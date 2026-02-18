package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"

	pb "P3/protofiles/payment_gateway"
)

type TransactionQueue struct{
	mu    sync.Mutex
	queue []*pb.PaymentRequest
}

var (
	queue      TransactionQueue
	clientConn *grpc.ClientConn
)

func main(){
	var username, password string


	fmt.Print("Enter username: ")
	fmt.Scanln(&username)
	fmt.Print("Enter password: ")
	fmt.Scanln(&password)


	initConnection()


	token := login(username, password)
	if token== ""{
		log.Fatal("Login failed")
	}


	go processTransactionQueue(token)


	for{
		var choice int

		fmt.Println("\nChoose operation:")
		fmt.Println("1. Check Balance")
		fmt.Println("2. Make Payment")
		fmt.Println("3. Exit")
		
		fmt.Print("Enter choice: ")

		fmt.Scanln(&choice)

		switch choice{
		case 1:
			checkBalance(token)
		case 2:
			makePayment(username,token)
		case 3:
			fmt.Println("Exiting...")
			return
		default:
			fmt.Println("Invalid choice")
		}
	}
}

func initConnection(){
	
	cert, err := tls.LoadX509KeyPair("certificates/admin-client.crt", "certificates/admin-client.key")
	
	if err!= nil{
	
		log.Fatal(err)
	}

	caCert, err := ioutil.ReadFile("certificates/ca.crt")
	
	
	if err!= nil{
		log.Fatal(err)
	}
	
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caCert)

	creds := credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ServerName:   "localhost",
		RootCAs:      caPool,
	})

	clientConn, err= grpc.Dial("localhost:50050", grpc.WithTransportCredentials(creds))
	
	if err!= nil{
		log.Println("Warning: Could not connect to gateway server. Some operations will be queued.")
	}
}

func login(username, password string) string{
	if clientConn== nil{
		fmt.Println("Server unavailable. Please try again later.")
		return ""
	}

	client := pb.NewPaymentGatewayClient(clientConn)
	resp, err := client.Login(context.Background(), &pb.LoginRequest{
		Username: username,
		Password: password,
	})

	if err!= nil{
		log.Printf("Login failed: %v", err)
		return ""
	}

	fmt.Println("\nLogin successful!")
	return resp.Token
}

func checkBalance(token string){
	if clientConn== nil{
		fmt.Println("Server unavailable. Balance check cannot be performed.")
		return
	}

	client := pb.NewPaymentGatewayClient(clientConn)
	ctx := metadata.AppendToOutgoingContext(context.Background(), "authorization", token)

	balance, err := client.ViewBalance(ctx, &pb.BalanceRequest{})
	if err!= nil{
		fmt.Printf("Balance check failed: %v\n", err)
		return
	}

	fmt.Printf("\nCurrent Balance: %.2f\n", balance.Balance)
}

func makePayment(username string, token string){
	var receiver string
	var amount float64

	fmt.Println("Sender: ", username)
	

	fmt.Print("Enter receiver username: ")
	fmt.Scanln(&receiver)
	fmt.Print("Enter amount: ")
	fmt.Scanln(&amount)

	txID := uuid.New().String()
	req := &pb.PaymentRequest{
		Sender:        username,
		Receiver:      receiver,
		Amount:        amount,
		TransactionId: txID,
	}


	if clientConn!= nil{
		ctx := createContext(token, txID)
		_, err := pb.NewPaymentGatewayClient(clientConn).ProcessPayment(ctx, req)
		if err== nil{
			fmt.Println("Payment processed successfully!")
			return
		}
	}


	
	queue.mu.Lock()
	
	queue.queue= append(queue.queue, req)
	
	queue.mu.Unlock()
	
	fmt.Println("Server unavailable. Payment has been queued and will be processed when connection is restored.")
}

func processTransactionQueue(token string){
	
	ticker := time.NewTicker(10 * time.Second)
	
	defer ticker.Stop()

	for range ticker.C{
		if clientConn== nil{
			initConnection()
			continue
		}

		queue.mu.Lock()
		if len(queue.queue)== 0{
			queue.mu.Unlock()
			continue
		}


		var failed []*pb.PaymentRequest
		for _, req := range queue.queue{
			ctx := createContext(token, req.TransactionId)

			_, err := pb.NewPaymentGatewayClient(clientConn).ProcessPayment(ctx, req)

			if err!= nil{
				failed= append(failed, req)
			}
		}

		queue.queue= failed

		queue.mu.Unlock()

		if len(failed)== 0{
			fmt.Println("\nAll queued transactions processed successfully!")
		}
	}
}

func createContext(token, txID string) context.Context{

	ctx := metadata.AppendToOutgoingContext(

		context.Background(),
		"authorization", token,
		"x-transaction-id", txID,
	)

	return ctx

}