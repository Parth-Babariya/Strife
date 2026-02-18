package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"log"
	"bufio"
	"os"
	"strconv"

	admin_pb "P3/protofiles/admin"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type User struct {
	ID       string
	Password string
	Bank     string
	Balance  float64
}

func main(){

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

	
	creds :=credentials.NewTLS(&tls.Config{
	
		Certificates: []tls.Certificate{cert},
		ServerName:   "localhost",
		RootCAs:      caPool,
	})

	conn, err :=grpc.Dial("localhost:50050", grpc.WithTransportCredentials(creds))
	
	if err !=nil{
		log.Fatal(err)
	}
	defer conn.Close()

	client :=admin_pb.NewAdminClient(conn)

	// users :=[]struct{
	// 	ID       string
	// 	Password string
	// 	Bank     string
	// 	Balance  float64
	// }{
	// 	{"user1", "pass1", "bank1", 1000.0},
	// 	{"user2", "pass2", "bank2", 2000.0},
	// 	{"user3", "pass3", "bank3", 3000.0},
	// }

	scanner := bufio.NewScanner(os.Stdin)
	var users []User

	fmt.Print("How many users do you want to add? ")
	var n int
	fmt.Scan(&n)

	for i := 0; i < n; i++ {
		fmt.Printf("\nEnter details for user %d:\n", i+1)

		fmt.Print("ID: ")
		scanner.Scan()
		id := scanner.Text()

		fmt.Print("Password: ")
		scanner.Scan()
		password := scanner.Text()

		fmt.Print("Bank: ")
		scanner.Scan()
		bank := scanner.Text()

		fmt.Print("Balance: ")
		scanner.Scan()
		balanceStr := scanner.Text()
		balance, err := strconv.ParseFloat(balanceStr, 64)
		if err != nil {
			fmt.Println("Invalid balance, defaulting to 0")
			balance = 0.0
		}

		users = append(users, User{id, password, bank, balance})
	}

	fmt.Println("\nUsers entered:")

	for _, u :=range users{
		resp, err :=client.AddUser(context.Background(), &admin_pb.UserRequest{
			Id:       u.ID,
			Password: u.Password,
			Bank:     u.Bank,
			Balance:  u.Balance,
		})
		if err !=nil{
			log.Printf("Failed to add user %s: %v", u.ID, err)
			continue
		}
		fmt.Printf("Added user %s: %s\n", u.ID, resp.Message)


	}


}