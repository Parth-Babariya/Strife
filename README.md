# Strife

Strife is a lightweight payment gateway inspired by Stripe, designed to interface with multiple bank servers to handle secure transactions. This project leverages **gRPC** for efficient inter-service communication and **TLS certificates** for secure connections.  

## Features  

- **Admin Service**: Allows administrators to add users to banks.  
- **Bank Service**: Manages user authentication, balance inquiries, deposits, withdrawals, and inter-bank transfers.  
- **Payment Gateway**: Handles user login, balance viewing, and transaction processing.  
- **Secure Communication**: Uses **TLS certificates** for encrypted connections between services.  
- **Two-Phase Commit (2PC)**: Ensures transaction consistency across multiple banks.  

## Directory Structure  

```
Strife/
│── P3/
│   ├── protofiles/          # Protobuf definitions for gRPC communication
│   │   ├── admin.proto
│   │   ├── bank.proto
│   │   ├── payment_gateway.proto
│   ├── certificates/        # TLS certificates for secure communication
│   ├── server/
│   │   ├── gateway/         # Payment gateway server
│   │   │   ├── gateway_server.go
│   │   ├── bank/            # Bank server instances
│   │   │   ├── bank_server.go
│   ├── client/
│   │   ├── admin/           # Admin client
│   │   │   ├── admin.go
│   │   ├── client/          # User client
│   │   │   ├── main.go

```

## Setup and Installation  

### 1. Generate gRPC Code  

Run the following command to generate Go code from `.proto` files:  

```sh
protoc --go_out=. --go-grpc_out=. protofiles/*.proto
```

### 2. Initialize Go Modules  

```sh
go mod init P3
go mod tidy
```

## 3. Generate TLS Certificates  

Ensure that you have **OpenSSL** installed, then manually generate the necessary certificates by running the following commands:  

```sh
# Step 1: Create CA private key and certificate
openssl genrsa -out certificates/ca.key 4096
openssl req -new -x509 -days 3650 -key certificates/ca.key -out certificates/ca.crt \
  -subj "/C=US/ST=California/L=San Francisco/O=MyCA/CN=Certificate Authority"

# Step 2: Generate server certificates (Gateway and Banks)
# Generate gateway server certificate
openssl genrsa -out certificates/gateway-server.key 4096
openssl req -new -key certificates/gateway-server.key -out certificates/gateway-server.csr \
  -subj "/C=US/ST=California/L=San Francisco/O=MyCompany/CN=localhost"

# Generate bank server certificate (repeat for multiple banks with different names)
openssl genrsa -out certificates/bank-server.key 4096
openssl req -new -key certificates/bank-server.key -out certificates/bank-server.csr \
  -subj "/C=US/ST=California/L=San Francisco/O=MyBank/CN=bank.localhost"

## Gateway Server Certificate
openssl genrsa -out certificates/gateway-server.key 4096
openssl req -new -key certificates/gateway-server.key -out certificates/gateway-server.csr \
  -subj "/C=US/ST=California/L=San Francisco/O=MyCompany/CN=localhost"
openssl x509 -req -days 365 -in certificates/gateway-server.csr \
  -CA certificates/ca.crt -CAkey certificates/ca.key -CAcreateserial \
  -out certificates/gateway-server.crt \
  -extfile <(printf "subjectAltName=DNS:localhost,IP:127.0.0.1")

## Bank Server Certificates (Bank1 & Bank2)
openssl genrsa -out certificates/bank-server.key 4096
openssl req -new -key certificates/bank-server.key -out certificates/bank-server.csr \
  -subj "/C=US/ST=California/L=San Francisco/O=MyBank/CN=bank.localhost" \
  -addext "subjectAltName=DNS:bank.localhost,DNS:localhost,IP:127.0.0.1"
openssl x509 -req -days 365 -in certificates/bank-server.csr \
  -CA certificates/ca.crt -CAkey certificates/ca.key -CAcreateserial \
  -out certificates/bank-server.crt \
  -extfile <(printf "subjectAltName=DNS:bank.localhost,DNS:localhost,IP:127.0.0.1")

openssl genrsa -out certificates/bank2-server.key 4096
openssl req -new -key certificates/bank2-server.key -out certificates/bank2-server.csr \
  -subj "/C=US/ST=California/L=San Francisco/O=MyBank/CN=bank2.localhost" \
  -addext "subjectAltName=DNS:bank2.localhost,DNS:localhost,IP:127.0.0.1"
openssl x509 -req -days 365 -in certificates/bank2-server.csr \
  -CA certificates/ca.crt -CAkey certificates/ca.key -CAcreateserial \
  -out certificates/bank2-server.crt \
  -extfile <(printf "subjectAltName=DNS:bank2.localhost,DNS:localhost,IP:127.0.0.1")

# Step 3: Generate Client Certificates (Regular & Admin)
openssl genrsa -out certificates/client.key 4096
openssl req -new -key certificates/client.key -out certificates/client.csr \
  -subj "/C=US/ST=California/L=San Francisco/O=MyCompany/CN=Client"
openssl x509 -req -days 365 -in certificates/client.csr \
  -CA certificates/ca.crt -CAkey certificates/ca.key -CAcreateserial \
  -out certificates/client.crt

openssl genrsa -out certificates/admin-client.key 4096
openssl req -new -key certificates/admin-client.key -out certificates/admin-client.csr \
  -subj "/C=US/ST=California/L=San Francisco/O=MyCompany/CN=Admin Client"
openssl x509 -req -days 365 -in certificates/admin-client.csr \
  -CA certificates/ca.crt -CAkey certificates/ca.key -CAcreateserial \
  -out certificates/admin-client.crt

# Step 4: Verify Certificates
openssl verify -CAfile certificates/ca.crt certificates/gateway-server.crt
openssl verify -CAfile certificates/ca.crt certificates/client.crt


```

These commands generate **self-signed TLS certificates** to enable secure **gRPC communication** between the payment gateway, banks, and clients.  



### 4. Run the Services  

Start the payment gateway server:  

```sh
go run server/gateway/gateway_server.go
```

Start multiple bank servers:  

```sh
go run server/bank/bank_server.go --name bank1 --port 50051
go run server/bank/bank_server.go --name bank2 --port 50052
```

Run the admin client to add users:  

```sh
go run client/admin/admin.go
```

Run the user client to interact with the payment system:  

```sh
go run client/client/main.go
```
