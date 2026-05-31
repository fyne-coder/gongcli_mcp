package main

import (
	"context"
	"log"

	"github.com/fyne-coder/gongcli_mcp/internal/gateway"
)

func main() {
	cfg, err := gateway.LoadConfig()
	if err != nil {
		log.Fatalf("gateway config: %v", err)
	}
	authorizer, err := gateway.NewRemoteAuthorizer(context.Background(), cfg)
	if err != nil {
		log.Fatalf("gateway authorizer: %v", err)
	}
	var server *gateway.Server
	if cfg.DCREnabled {
		cognitoStore, err := gateway.NewCognitoClientStore(context.Background(), cfg)
		if err != nil {
			log.Fatalf("gateway Cognito DCR: %v", err)
		}
		authorizer = gateway.NewAuthorizerWithClientVerifier(cfg, authorizer.KeyFunc(), cognitoStore)
		server = gateway.NewServerWithDCR(cfg, authorizer, cognitoStore)
	} else {
		server = gateway.NewServer(cfg, authorizer)
	}
	log.Printf("gongmcp gateway listening %s", server.LogConfig())
	if err := gateway.NewHTTPServer(cfg.Addr, server.Handler()).ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
