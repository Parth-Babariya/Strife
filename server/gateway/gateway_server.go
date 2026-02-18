package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	admin_pb "P3/protofiles/admin"
	bank_pb "P3/protofiles/bank"
	pb "P3/protofiles/payment_gateway"

	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"

	// "google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)
var (
    grpcServer   *grpc.Server
    serverLis    net.Listener
    serverLock   sync.Mutex
    serverActive bool
)

type PaymentGatewayServer struct{
	pb.UnimplementedPaymentGatewayServer
	userBankMap map[string]string
	transactions sync.Map
	bankClients  map[string]bank_pb.BankClient
	mu          sync.RWMutex
    userStore    map[string]*bank_pb.User
}


type TransactionRecord struct{
	Response  interface{}
	Timestamp time.Time
}


// type AdminServer struct{
// 	admin_pb.UnimplementedAdminServer
// 	gateway *PaymentGatewayServer
// }

type AdminServer struct{
	admin_pb.UnimplementedAdminServer

	bankClients map[string]bank_pb.BankClient
	gateway     *PaymentGatewayServer
}

func (s *PaymentGatewayServer) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error){
    
    logEvent("GATEWAY", "AUTH", "Login attempt", "username", req.Username)

    
    s.mu.RLock()
    
    bank, exists :=s.userBankMap[req.Username]
    
    s.mu.RUnlock()
    
    if !exists{
    
        logEvent("GATEWAY", "ERROR", "User not registered", "username", req.Username)
        return nil, status.Error(codes.NotFound, "user not found")
    }

    
    bankClient, ok :=s.bankClients[bank]
    if !ok{
        logEvent("GATEWAY", "ERROR", "Bank connection failed", "bank", bank)
        return nil, status.Error(codes.Internal, "bank service unavailable")
    
    }

    



    validationResp, err :=bankClient.ValidateUser(ctx, &bank_pb.Credentials{
        Username: req.Username,
        Password: req.Password,
    })
    if err !=nil|| !validationResp.Valid{
        logEvent("GATEWAY", "AUTH", "Invalid credentials", "username", req.Username, "error", err)
        return nil, status.Error(codes.Unauthenticated, "invalid credentials")
    }

    
    token :=jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
        "username": req.Username,
        "bank":     bank,
        "exp":      time.Now().Add(5 * time.Minute).Unix(),
    })
    
    tokenString, err :=token.SignedString([]byte("secret"))
    if err !=nil{
        logEvent("GATEWAY", "ERROR", "Token generation failed", "error", err)
        return nil, status.Errorf(codes.Internal, "failed to generate token")
    }

    logEvent("GATEWAY", "AUTH", "Login successful", "username", req.Username)
    return &pb.LoginResponse{Token: tokenString}, nil
}

func (s *AdminServer) AddUser(ctx context.Context, req *admin_pb.UserRequest) (*admin_pb.UserResponse, error){
    logEvent("ADMIN", "USER", "Add user request",
        "user_id", req.Id,
        "bank", req.Bank,
    )

    client, exists :=s.bankClients[req.Bank]
    if !exists{
        logEvent("ADMIN", "ERROR", "Bank not found",
            "bank", req.Bank,
        )
        return nil, status.Errorf(codes.NotFound, "bank %s not found", req.Bank)
    }
    

    // adding the user 
    _, err :=client.AddUser(ctx, &bank_pb.User{
        Id:       req.Id,
        Password: req.Password,
        Balance:  req.Balance,
    })

    if err !=nil{
        logEvent("ADMIN", "ERROR", "Add user failed",
            "user_id", req.Id,
            "error", err,
        )
        return &admin_pb.UserResponse{
            Success: false,
            Message: fmt.Sprintf("Failed to add user: %v", err),
        }, nil
    }

    
    
    
    // concurrency control
    s.gateway.mu.Lock()
    
    // s.gateway.userStore[req.Id]=&bank_pb.User{
    //     Id:       req.Id,
    //     Password: req.Password,
    //     Balance:  req.Balance,
    // }
    s.gateway.userBankMap[req.Id]=req.Bank
    
    s.gateway.mu.Unlock()

    logEvent("ADMIN", "SUCCESS", "User added",
        "user_id", req.Id,
        "bank", req.Bank,
        "balance", req.Balance,
    )

    
    return &admin_pb.UserResponse{
        Success: true,
        Message: "User added successfully 🎉",
    }, nil
}


func authInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error){
    
    if info.FullMethod =="/payment_gateway.PaymentGateway/Login" || info.FullMethod =="/admin.Admin/AddUser"{
    
        return handler(ctx, req)
    }

    md, ok :=metadata.FromIncomingContext(ctx)
    if !ok{
        return nil, status.Errorf(codes.Unauthenticated, "metadata not provided")
    }

    tokens :=md.Get("authorization")
    if len(tokens) ==0{
        return nil, status.Errorf(codes.Unauthenticated, "authorization token missing")
    }

    tokenString :=tokens[0]
    token, err :=jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error){
        if _, ok :=token.Method.(*jwt.SigningMethodHMAC); !ok{
            return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
        }
        return []byte("secret"), nil
    })

    if err !=nil || !token.Valid{
        logEvent("GATEWAY", "AUTH", "Invalid token", "error", err)
        return nil, status.Errorf(codes.Unauthenticated, "invalid token")
    }

    // Check expiration
    if claims, ok :=token.Claims.(jwt.MapClaims); ok{
        if exp, ok :=claims["exp"].(float64); ok{
            if time.Now().Unix() > int64(exp){
                logEvent("GATEWAY", "AUTH", "Token expired", "username", claims["username"])
                return nil, status.Errorf(codes.Unauthenticated, "token expired")
            }
        }
    }

    return handler(ctx, req)
}

func NewAdminServer(gateway *PaymentGatewayServer) *AdminServer{
    admin :=&AdminServer{
        bankClients: make(map[string]bank_pb.BankClient),
            gateway:     gateway,
        }
    



	banks :=map[string]string{
		"bank1": "localhost:50051",
		"bank2": "localhost:50052",
        "bank3": "localhost:50053",
	}









	for bankName, addr :=range banks{
		cert, err :=tls.LoadX509KeyPair("certificates/admin-client.crt", "certificates/admin-client.key")
		if err !=nil{
			log.Fatal(err)
		}

		caCert, err :=ioutil.ReadFile("certificates/ca.crt")
		if err !=nil{
			log.Fatal(err)
		}
		caPool :=x509.NewCertPool()
		caPool.AppendCertsFromPEM(caCert)

		tlsConfig :=&tls.Config{
			Certificates: []tls.Certificate{cert},
			RootCAs:      caPool,
		}

		conn, err :=grpc.Dial(addr,
			grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		)
		if err !=nil{
			log.Fatalf("Failed to connect to %s: %v", bankName, err)
		}
		admin.bankClients[bankName]=bank_pb.NewBankClient(conn)
	}

	return admin
}

//function for logging events time event type and message
func logEvent(component, eventType, message string, fields ...interface{}){
	timestamp :=time.Now().Format("2006-01-02 15:04:05")
	log.Printf("[%s][%s][%s] %s %v", timestamp, component, eventType, message, fields)
}




func (s *PaymentGatewayServer) ProcessPayment(ctx context.Context, req *pb.PaymentRequest) (*pb.PaymentResponse, error){
    
    logEvent("PAYMENT", "START", "🚀 Processing payment",
        "tx_id", req.TransactionId,
        "sender", req.Sender,
        "receiver", req.Receiver,
        "amount", req.Amount,
    )

    md, _ :=metadata.FromIncomingContext(ctx)
    txIDs :=md.Get("x-transaction-id")
    if len(txIDs) ==0{
        // fmt.Println("Transaction ID not found in metadata")
        return nil, status.Error(codes.InvalidArgument, "transaction ID required")
    }
    // txID :=txIDs[0]

    if req.TransactionId ==""{
        logEvent("PAYMENT", "ERROR", "Missing transaction ID")
        // fmt.Println("Transaction ID is empty")
        return nil, status.Error(codes.InvalidArgument, "transaction ID required")
    }

    if record, exists :=s.transactions.Load(req.TransactionId); exists{
        logEvent("PAYMENT", "DUPLICATE", "Duplicate transaction detected",
            "tx_id", req.TransactionId,
        )
        // fmt.Println("Duplicate transaction detected for ID:", req.TransactionId)
        return record.(TransactionRecord).Response.(*pb.PaymentResponse), nil
    }

    clientIP :="unknown"
    if p, ok :=peer.FromContext(ctx); ok{
        clientIP=p.Addr.String()
        // fmt.Println("Client IP extracted from context:", clientIP)
    }

    s.mu.RLock()
    senderBank, senderOk :=s.userBankMap[req.Sender]
    receiverBank, receiverOk :=s.userBankMap[req.Receiver]
    s.mu.RUnlock()
    // fmt.Println("Sender Bank:", senderBank, "Exists:", senderOk)
    // fmt.Println("Receiver Bank:", receiverBank, "Exists:", receiverOk)

    if !senderOk || !receiverOk{
        logEvent("PAYMENT", "ERROR", "User bank not found",
            "sender_exists", senderOk,
            "receiver_exists", receiverOk,
            "client", clientIP,
        )
        // fmt.Println("Sender exists:", senderOk, "Receiver exists:", receiverOk)
        return nil, status.Error(codes.NotFound, "one or more users not found")
    }

    // Get sender and receiver bank clients
    senderClient, sok :=s.bankClients[senderBank]
    receiverClient, rok :=s.bankClients[receiverBank]
    if !sok || !rok{
        logEvent("PAYMENT", "ERROR", "Bank connection failed",
            "sender_bank", senderBank,
            "receiver_bank", receiverBank,
            "client", clientIP,
        )
        // fmt.Println("Sender bank client exists:", sok, "Receiver bank client exists:", rok)
        return nil, status.Error(codes.Internal, "bank service unavailable")
    }


    debitTxID :=uuid.New().String()
    creditTxID :=uuid.New().String()
    // fmt.Println("Generated Debit TxID:", debitTxID, "Credit TxID:", creditTxID)


    debitCtx :=metadata.AppendToOutgoingContext(ctx, "x-transaction-id", debitTxID)
    debitResp, err :=senderClient.Debit(debitCtx, &bank_pb.DebitRequest{
        Username: req.Sender,
        Amount:   req.Amount,
    })
    // fmt.Println("Debit Response:", debitResp, "Error:", err)

	print("debitResp: ", debitResp)
    if err !=nil{
        logEvent("PAYMENT", "ERROR", "Debit failed",
            "sender", req.Sender,
            "amount", req.Amount,
            "error", err,
            "client", clientIP,
        )
        return nil, status.Errorf(codes.Internal, "debit failed: %v", err)
    }
    logEvent("PAYMENT", "DEBIT", "💸 Amount debited",
        "sender", req.Sender,
        "amount", req.Amount,
        "bank", senderBank,
        "client", clientIP,
    )

	
    creditCtx :=metadata.AppendToOutgoingContext(ctx, "x-transaction-id", creditTxID)
    creditResp, err :=receiverClient.Credit(creditCtx, &bank_pb.CreditRequest{
        Username: req.Receiver,
        Amount:   req.Amount,
    })
	print("creditResp: ", creditResp)
    if err !=nil{
        logEvent("PAYMENT", "ERROR", "Credit failed",
            "receiver", req.Receiver,
            "amount", req.Amount,
            "error", err,
            "client", clientIP,
        )
        

        _, refundErr :=senderClient.Credit(ctx, &bank_pb.CreditRequest{
            Username: req.Sender,
            Amount:   req.Amount,
        })
        if refundErr !=nil{
            logEvent("PAYMENT", "CRITICAL", "Refund failed",
                "sender", req.Sender,
                "amount", req.Amount,
                "error", refundErr,
                "client", clientIP,
            )
            return nil, status.Errorf(codes.Internal, 
                "credit failed and refund failed: %v (refund: %v)", err, refundErr)
        }
        
        logEvent("PAYMENT", "WARNING", "Credit failed but refund succeeded",
            "client", clientIP,
            "tx_id", req.TransactionId,
        )
        return nil, status.Errorf(codes.Internal, "credit failed but refund succeeded: %v", err)
    }

    logEvent("PAYMENT", "CREDIT", "💰 Amount credited",
        "receiver", req.Receiver,
        "amount", req.Amount,
        "bank", receiverBank,
        "client", clientIP,
    )

    response :=&pb.PaymentResponse{
        Success: true,
        Message: fmt.Sprintf("Successfully transferred %.2f from %s to %s", 
            req.Amount, req.Sender, req.Receiver),
    }


    s.transactions.Store(req.TransactionId, TransactionRecord{
        Response:  response,
        Timestamp: time.Now(),
    })

    logEvent("PAYMENT", "SUCCESS", "✅ Payment completed 🎉",
        "tx_id", req.TransactionId,
        "amount", req.Amount,
        "client", clientIP,
        "sender_bank", senderBank,
        "receiver_bank", receiverBank,
    )

    return response, nil
}

func (s *PaymentGatewayServer) ViewBalance(ctx context.Context, req *pb.BalanceRequest) (*pb.BalanceResponse, error){

    md, ok :=metadata.FromIncomingContext(ctx)

    if !ok{
        return nil, status.Error(codes.Unauthenticated, "metadata not found")
    }
    
    tokens :=md.Get("authorization")
    if len(tokens) ==0{
        return nil, status.Error(codes.Unauthenticated, "authorization token missing")
    }
    
    token, err :=jwt.Parse(tokens[0], func(token *jwt.Token) (interface{}, error){
        if _, ok :=token.Method.(*jwt.SigningMethodHMAC); !ok{
            return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
        }
        return []byte("secret"), nil
    })
    
    if err !=nil || !token.Valid{
        return nil, status.Error(codes.Unauthenticated, "invalid token")
    }
    
    claims, ok :=token.Claims.(jwt.MapClaims)
    if !ok{
        return nil, status.Error(codes.Unauthenticated, "invalid token claims")
    }
    
    username, ok :=claims["username"].(string)
    if !ok{
        return nil, status.Error(codes.Unauthenticated, "username not found in token")
    }


    s.mu.RLock()
    bank, exists :=s.userBankMap[username]
    s.mu.RUnlock()
    
    if !exists{
        logEvent("BALANCE", "ERROR", "User bank not found", "username", username)
        return nil, status.Error(codes.NotFound, "user not found")
    }


    bankClient, ok :=s.bankClients[bank]
    if !ok{
        logEvent("BALANCE", "ERROR", "Bank connection failed", "bank", bank)
        return nil, status.Error(codes.Internal, "bank service unavailable")
    }


    balanceResp, err :=bankClient.GetBalance(ctx, &bank_pb.BalanceQuery{

        Username: username,
    })
    
    if err !=nil{
        logEvent("BALANCE", "ERROR", "Bank balance check failed", 
            "username", username, "bank", bank, "error", err)
        return nil, status.Errorf(codes.Internal, "failed to get balance: %v", err)
    }

    logEvent("BALANCE", "SUCCESS", "💰 Balance retrieved 🎉", 
        "username", username, "balance", balanceResp.Amount)
    return &pb.BalanceResponse{Balance: balanceResp.Amount}, nil
}


func main(){
	cert, err :=tls.LoadX509KeyPair("certificates/gateway-server.crt", "certificates/gateway-server.key")
	if err !=nil{
		log.Fatalf("Failed to load server certificate: %v", err)
	}

	caCert, err :=ioutil.ReadFile("certificates/ca.crt")
	if err !=nil{
		log.Fatalf("Failed to read CA certificate: %v", err)
	}
	caPool :=x509.NewCertPool()
	caPool.AppendCertsFromPEM(caCert)

	creds :=credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
        MinVersion:   tls.VersionTLS12,
	})



    banks :=map[string]string{
        "bank1": "localhost:50051",
        "bank2": "localhost:50052",
        "bank3": "localhost:50053",
    }



    controlChan :=make(chan string)
    go func(){
        // disable date tiem in logging
        log.SetFlags(0) 


        scanner :=bufio.NewScanner(os.Stdin)
        for scanner.Scan(){
            cmd :=strings.ToLower(scanner.Text())
            switch cmd{
            case "stop", "start", "exit":
                controlChan <- cmd
            default:
                log.Println("Unknown command. Valid commands: stop, start, exit")
            }
        }
    }()

    var server *grpc.Server
    var lis net.Listener
    var wg sync.WaitGroup
    state :="stopped"
    log.Println("Write \"start\" to start the server")

    for{

        select{

        case cmd :=<-controlChan:
            switch cmd{

            case "stop":
                if state =="running"{
                    log.Println("🛑 Stopping server...")
                    if server !=nil{
                        server.GracefulStop()
                    }
                    if lis !=nil{
                        lis.Close()
                    }
                    state="stopped"
                    log.Println("🛑 Server stopped. Enter '▶️ start' to restart")
                }

            case "start":
                if state =="stopped"{
                    
                    log.Println("🔄 Restarting server...")
                    
                    var err error
                    lis, err=net.Listen("tcp", ":50050")
                    if err !=nil{
                        log.Printf("Failed to create listener: %v", err)
                        state="stopped"
                        continue
                    }

                    gateway :=&PaymentGatewayServer{
                        userBankMap:  make(map[string]string),
                        bankClients: make(map[string]bank_pb.BankClient),
                        userStore:   make(map[string]*bank_pb.User),
                    }
                    
                    for bankName, addr :=range banks{
                        clientCert, err :=tls.LoadX509KeyPair("certificates/admin-client.crt","certificates/admin-client.key",)
                        if err !=nil{
                            log.Fatalf("Failed to load client cert: %v", err)
                        }

                        bankCreds :=credentials.NewTLS(&tls.Config{
                            Certificates: []tls.Certificate{clientCert},
                            ServerName:   "localhost",
                            RootCAs:      caPool,
                        })

                        conn, err :=grpc.Dial(addr, grpc.WithTransportCredentials(bankCreds))
                        if err !=nil{
                            log.Fatalf("Failed to connect to %s: %v", bankName, err)
                        }
                        gateway.bankClients[bankName]=bank_pb.NewBankClient(conn)
                        client :=bank_pb.NewBankClient(conn)
                        users, err :=client.GetAllUsers(context.Background(), &bank_pb.Empty{})
                        if err ==nil{
                            for _, user :=range users.GetUsers(){
                                gateway.userStore[user.Id]=user
                                gateway.userBankMap[user.Id]=bankName
                            }
                        }
                    }

                    server=grpc.NewServer(
                        grpc.Creds(creds),
                        grpc.ChainUnaryInterceptor(
                            authInterceptor,
                        ),
                    )
                    
                    adminServer :=NewAdminServer(gateway)
                    pb.RegisterPaymentGatewayServer(server, gateway)
                    admin_pb.RegisterAdminServer(server, adminServer)

                    wg.Add(1)
                    go func(){
                        defer wg.Done()
                        logEvent("SYSTEM", "STARTUP", "🚀 Gateway server starting", "port", 50050)
                        if err :=server.Serve(lis); err !=nil{
                            log.Printf("❌ Server error: %v", err)
                        }
                    }()
                    
                    state="running"
                    log.Println("✅ Server started 🎉")
                }

            // case "exit":
            //     log.Println("Shutting down...")
            //     if server !=nil{
            //         server.GracefulStop()
            //     }
            //     if lis !=nil{
            //         lis.Close()
            //     }
            //     return
            }
        }
    }
}