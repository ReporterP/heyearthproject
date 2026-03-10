#!/bin/bash
set -e

OUT_DIR="../certs"
mkdir -p $OUT_DIR

echo "Generating CA..."
openssl req -x509 -newkey rsa:4096 -days 3650 -nodes -keyout $OUT_DIR/ca-key.pem -out $OUT_DIR/ca-cert.pem -subj "/C=RU/ST=Moscow/L=Moscow/O=HeYearth/OU=RootCA/CN=HeYearth Root CA"

echo "Generating Server (Node Agent) cert..."
openssl req -newkey rsa:4096 -nodes -keyout $OUT_DIR/server-key.pem -out $OUT_DIR/server-req.pem -subj "/C=RU/ST=Moscow/L=Moscow/O=HeYearth/OU=NodeAgent/CN=node-agent"
openssl x509 -req -in $OUT_DIR/server-req.pem -days 3650 -CA $OUT_DIR/ca-cert.pem -CAkey $OUT_DIR/ca-key.pem -CAcreateserial -out $OUT_DIR/server-cert.pem -extfile <(printf "subjectAltName=IP:127.0.0.1,DNS:localhost,DNS:node-agent")

echo "Generating Client (Main Server API) cert..."
openssl req -newkey rsa:4096 -nodes -keyout $OUT_DIR/client-key.pem -out $OUT_DIR/client-req.pem -subj "/C=RU/ST=Moscow/L=Moscow/O=HeYearth/OU=MainAPI/CN=main-api"
openssl x509 -req -in $OUT_DIR/client-req.pem -days 3650 -CA $OUT_DIR/ca-cert.pem -CAkey $OUT_DIR/ca-key.pem -CAcreateserial -out $OUT_DIR/client-cert.pem

rm $OUT_DIR/*-req.pem
echo "Done. Certificates are in $OUT_DIR"
