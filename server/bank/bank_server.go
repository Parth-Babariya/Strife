package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"

	"fmt"
	"io/ioutil"
	"log"
	"net"
	"sync"
	"time"

	pb "P3/protofiles/bank"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"golang.org/x/crypto/bcrypt"
)

type TransactionRecord struct{
	Response interface{}
	Timestamp time.Time
}

var (
	bankName = flag.String("name", "", "Bank name (e.g., bank1, bank2, etc.)")
	bankPort = flag.String("port", "50051", "Port to listen on")
)

type BankServer struct{
	pb.UnimplementedBankServer
	users map[string]*pb.User
	transactions sync.Map
	mu    sync.Mutex
	name  string
}




func loggingInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error){
	start := time.Now()
	

	clientIP := "unknown"
	if p, ok := peer.FromContext(ctx); ok{
		clientIP = p.Addr.String()
	}


	log.Printf("[%s:%s] Received %s request from %s: %+v", 
		*bankName, *bankPort, info.FullMethod, clientIP, req)


	resp, err := handler(ctx, req)

	duration := time.Since(start)
	st, _ := status.FromError(err)
	log.Printf("[%s:%s] Completed %s => Status: %s, Duration: %v, Error: %v", 
		*bankName, *bankPort, info.FullMethod, st.Code(), duration, err)

	return resp, err
}

func HashPasswordBCrypt(password string) (string, error) {
    // cost: bcrypt.DefaultCost (10) is fine; increase for greater hardness (11-14)
    hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
    if err != nil {
        return "", err
    }
    return string(hash), nil
}

// CheckPasswordBCrypt compares hashed and plain password
func CheckPasswordBCrypt(hashed string, password string) bool {
    err := bcrypt.CompareHashAndPassword([]byte(hashed), []byte(password))
    return err == nil
}

func (s *BankServer) AddUser(ctx context.Context, user *pb.User) (*pb.UserResponse, error){
	s.mu.Lock()
	defer s.mu.Unlock()
	

	md, _ := metadata.FromIncomingContext(ctx)
	traceID := "none"
	if trace := md.Get("x-trace-id"); len(trace) > 0{
		traceID = trace[0]
	}

	log.Printf("[%s:%s] AddUser transaction - TraceID: %s, UserID: %s", 
		*bankName, *bankPort, traceID, user.Id)

	if _, exists := s.users[user.Id]; exists{
		return &pb.UserResponse{Success: false, Message: "User already exists"}, nil
	}
	// s.users[user.Id] = user
	// return &pb.UserResponse{Success: true, Message: "User added successfully"}, nil
	hashed, err := HashPasswordBCrypt(user.Password)
    if err != nil {
        return &pb.UserResponse{Success: false, Message: "internal error"}, nil
    }
    user.Password = hashed
    s.users[user.Id] = user
    return &pb.UserResponse{Success: true, Message: "User added successfully"}, nil

}

func (s *BankServer) ValidateUser(ctx context.Context, creds *pb.Credentials) (*pb.ValidationResponse, error){
	s.mu.Lock()
	defer s.mu.Unlock()

	clientIP := "unknown"
	if p, ok := peer.FromContext(ctx); ok{
		clientIP = p.Addr.String()
	}

	log.Printf("[%s:%s] ValidateUser attempt from %s for user: %s", 
		*bankName, *bankPort, clientIP, creds.Username)

	// fmt.Printf("ValidateUser called with username: %s, clientIP: %s\n", creds.Username, clientIP)

	user, exists := s.users[creds.Username]
	if !exists {
		// fmt.Printf("Validation failed for user: %s\n", creds.Username)
		return &pb.ValidationResponse{Valid: false}, nil
	}

	if !CheckPasswordBCrypt(user.Password, creds.Password) {
		// fmt.Printf("Validation failed for user: %s\n", creds.Username)
		return &pb.ValidationResponse{Valid: false}, nil
	}

	// fmt.Printf("Validation succeeded for user: %s\n", creds.Username)
	return &pb.ValidationResponse{Valid: true}, nil
}

func (s *BankServer) Debit(ctx context.Context, req *pb.DebitRequest) (*pb.TransactionResponse, error){

	
	md, _ := metadata.FromIncomingContext(ctx)
	// fmt.Printf("Metadata extracted: %+v\n", md) // Debug: Print extracted metadata

	
	txIDs := md.Get("x-transaction-id")
	// fmt.Printf("Transaction IDs found: %v\n", txIDs) // Debug: Print transaction IDs

	if len(txIDs) == 0 {
		// fmt.Println("No transaction ID provided in metadata") // Debug: Log missing transaction ID
		return nil, status.Error(codes.InvalidArgument, "transaction ID required")
	}
	txID := txIDs[0]
	// fmt.Printf("Using transaction ID: %s\n", txID) // Debug: Log the transaction ID being used

	
	if val, exists := s.transactions.Load(txID); exists {
		record := val.(TransactionRecord)
		// fmt.Printf("Existing transaction found: %+v\n", record) // Debug: Log existing transaction details

		if time.Since(record.Timestamp) < 24*time.Hour {
			// fmt.Println("Returning cached transaction response") // Debug: Log cached response usage
			return record.Response.(*pb.TransactionResponse), nil
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	
	user, exists := s.users[req.Username]
	if !exists{
		return nil, status.Errorf(codes.NotFound, "user not found")
	}

	if user.Balance < req.Amount{
		return nil, status.Errorf(codes.FailedPrecondition, "insufficient funds")
	}

	user.Balance -= req.Amount
	response := &pb.TransactionResponse{
		Success: true,
		Message: fmt.Sprintf("Debited %.2f from %s", req.Amount, req.Username),
	}

	
	s.transactions.Store(txID, TransactionRecord{
		Response:  response,
		Timestamp: time.Now(),
	})

	return response, nil
}

func (s *BankServer) Credit(ctx context.Context, req *pb.CreditRequest) (*pb.TransactionResponse, error){

	md, _ := metadata.FromIncomingContext(ctx)
	// fmt.Printf("Metadata extracted: %+v\n", md) // Debug: Print extracted metadata

	txIDs := md.Get("x-transaction-id")
	// fmt.Printf("Transaction IDs found: %v\n", txIDs) // Debug: Print transaction IDs

	if len(txIDs) == 0{
		// fmt.Println("No transaction ID provided in metadata") // Debug: Log missing transaction ID
		return nil, status.Error(codes.InvalidArgument, "transaction ID required")
	}
	txID := txIDs[0]
	// fmt.Printf("Using transaction ID: %s\n", txID) // Debug: Log the transaction ID being used

	if val, exists := s.transactions.Load(txID); exists{
		record := val.(TransactionRecord)
		// fmt.Printf("Existing transaction found: %+v\n", record) // Debug: Log existing transaction details

		if time.Since(record.Timestamp) < 24*time.Hour{
			// fmt.Println("Returning cached transaction response") // Debug: Log cached response usage
			return record.Response.(*pb.TransactionResponse), nil
		}
	}

    s.mu.Lock()
    defer s.mu.Unlock()
	


    user, exists := s.users[req.Username]
    if !exists{
        return nil, status.Errorf(codes.NotFound, "user not found")
    }
	log.Printf("[%s:%s] Starting credit for %s, current balance: %.2f", *bankName, *bankPort, req.Username, user.Balance)

    user.Balance += req.Amount
    response := &pb.TransactionResponse{
        Success: true,
        Message: fmt.Sprintf("Credited %.2f to %s", req.Amount, req.Username),
    }
	log.Printf("[%s:%s] Updated balance after credit: %.2f", *bankName, *bankPort, user.Balance)


    s.transactions.Store(txID, TransactionRecord{
        Response:  response,
        Timestamp: time.Now(),
    })

    return response, nil
}

func (s *BankServer) ProcessTransfer(ctx context.Context, req *pb.TransferRequest) (*pb.TransferResponse, error){

	md, _ := metadata.FromIncomingContext(ctx)
	// fmt.Printf("Metadata extracted: %+v\n", md) // Debug: Print extracted metadata

	txIDs := md.Get("x-transaction-id")
	// fmt.Printf("Transaction IDs found: %v\n", txIDs) // Debug: Print transaction IDs

	if len(txIDs) == 0 {
		// fmt.Println("No transaction ID provided in metadata") // Debug: Log missing transaction ID
		return nil, status.Error(codes.InvalidArgument, "transaction ID required")
	}
	txID := txIDs[0]
	// fmt.Printf("Using transaction ID: %s\n", txID) // Debug: Log the transaction ID being used

	if val, exists := s.transactions.Load(txID); exists {
		record := val.(TransactionRecord)
		// fmt.Printf("Existing transaction found: %+v\n", record) // Debug: Log existing transaction details

		if time.Since(record.Timestamp) < 24*time.Hour {
			// fmt.Println("Returning cached transaction response") // Debug: Log cached response usage
			return record.Response.(*pb.TransferResponse), nil
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sender, exists := s.users[req.Sender]
	if !exists {
		// fmt.Printf("Sender not found: %s\n", req.Sender) // Debug: Log sender not found
		return nil, status.Errorf(codes.NotFound, "sender not found")
	}
	// fmt.Printf("Sender found: %s, current balance: %.2f\n", req.Sender, sender.Balance) // Debug: Log sender details


	if sender.Balance < req.Amount{


		return nil, status.Errorf(codes.FailedPrecondition, "insufficient funds")
    }
    sender.Balance -= req.Amount


    s.transactions.Store(txID, TransactionRecord{
        Response: &pb.TransferResponse{
            Success: false,
            Message: "Transfer in progress",
        },
        Timestamp: time.Now(),
    })



    receiver, exists := s.users[req.Receiver]
    if !exists{

        sender.Balance += req.Amount
        return nil, status.Errorf(codes.NotFound, "receiver not found")
    }
    receiver.Balance += req.Amount


    response := &pb.TransferResponse{
        Success: true,
        Message: fmt.Sprintf("Transferred %.2f from %s to %s", req.Amount, req.Sender, req.Receiver),
    }
    s.transactions.Store(txID, TransactionRecord{
        Response:  response,
        Timestamp: time.Now(),
    })

    return response, nil
}

func (s *BankServer) GetBalance(ctx context.Context, req *pb.BalanceQuery) (*pb.Balance, error){
    s.mu.Lock()
    defer s.mu.Unlock()


    clientIP := "unknown"

	if p, ok := peer.FromContext(ctx); ok{
        clientIP = p.Addr.String()
    }

    log.Printf("[%s:%s] GetBalance request from %s for user: %s", *bankName, *bankPort, clientIP, req.Username)

    user, exists := s.users[req.Username]
    if !exists{
        log.Printf("[%s:%s] Balance check failed - user not found: %s", *bankName, *bankPort, req.Username)
        return nil, status.Errorf(codes.NotFound, "user not found")
    }

    log.Printf("[%s:%s] Balance returned for user %s: %.2f", *bankName, *bankPort, req.Username, user.Balance)
	return &pb.Balance{Amount: user.Balance}, nil
}

func (s *BankServer) GetAllUsers(ctx context.Context, _ *pb.Empty) (*pb.UserList, error){
	s.mu.Lock()
	defer s.mu.Unlock()

	// fmt.Printf("GetAllUsers called, current user count: %d\n", len(s.users)) // Debug: Log the number of users

	users := make([]*pb.User, 0, len(s.users))
	for _, user := range s.users{
		// fmt.Printf("Adding user to list: %s\n", user.Id) // Debug: Log each user being added
		users = append(users, user)
	}

	// fmt.Printf("Returning user list with %d users\n", len(users)) // Debug: Log the final user list size
	return &pb.UserList{Users: users}, nil
}

func main(){

	flag.Parse()
	
	if *bankName == ""{
		log.Fatal("Bank name must be specified with --name flag")
	}

	cert, err := tls.LoadX509KeyPair("certificates/bank-server.crt", "certificates/bank-server.key")
	if err != nil{
		log.Fatalf("Failed to load server certificate: %v", err)
	}

	caCert, err := ioutil.ReadFile("certificates/ca.crt")

	if err != nil{
		log.Fatalf("Failed to read CA certificate: %v", err)
	}
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caCert)

	creds := credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
	})


	server := grpc.NewServer(
		grpc.Creds(creds),
		grpc.UnaryInterceptor(loggingInterceptor),
	)

	bankServer := &BankServer{
		users: make(map[string]*pb.User),
		name:  *bankName,
	}

	pb.RegisterBankServer(server, bankServer)

	lis, err := net.Listen("tcp", ":"+*bankPort)

	if err != nil{
		log.Fatalf("Failed to listen: %v", err)
	}

	log.Printf("[%s:%s] Bank server starting...", *bankName, *bankPort)

	if err := server.Serve(lis); err != nil{
		log.Fatalf("Failed to serve: %v", err)
	}
}