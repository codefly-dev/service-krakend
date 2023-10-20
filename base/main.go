package main

import (
	"context"
	"github.com/codefly-dev/go-grpc/base/adapters"
	codefly "github.com/codefly-dev/sdk-go"
)

func main() {
	codefly.WithTrace()
	config := &adapters.Configuration{
		EndpointGrpc: codefly.Endpoint("self::grpc").PortAddress(),
	}
	if codefly.Endpoint("self::http").IsPresent() {
		config.EndpointHttp = codefly.Endpoint("self::http").PortAddress()
	}

	server, err := adapters.NewServer(config)
	if err != nil {
		panic(err)
	}
	err = server.Start(context.Background())
	if err != nil {
		panic(err)
	}
}
