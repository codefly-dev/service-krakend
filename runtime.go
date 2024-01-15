package main

import (
	"context"
	"fmt"
	"github.com/codefly-dev/core/agents/network"
	"github.com/codefly-dev/core/configurations"
	agentv0 "github.com/codefly-dev/core/generated/go/services/agent/v0"
	runtimev0 "github.com/codefly-dev/core/generated/go/services/runtime/v0"
	"github.com/codefly-dev/core/runners"
	"github.com/codefly-dev/core/wool"
	"strings"
)

type Runtime struct {
	*Service

	// internal
	Runner *runners.Docker
}

type Auth struct {
	Protected bool `yaml:"protected"`
}

// RestRoute extends the concept of RestRoute to add Auth aspects
type RestRoute = configurations.ExtendedRestRoute[Auth]

func NewRuntime() *Runtime {
	return &Runtime{
		Service: NewService(),
	}
}

func (s *Runtime) Load(ctx context.Context, req *runtimev0.LoadRequest) (*runtimev0.LoadResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	err := s.Base.Load(ctx, req.Identity, s.Settings)
	if err != nil {
		return s.Base.Runtime.LoadError(err)
	}

	s.RoutesLocation = s.Local("routing")

	err = s.LoadEndpoints(ctx)
	if err != nil {
		return s.Base.Runtime.LoadError(err)
	}

	// From configurations
	err = s.LoadRoutes(ctx)
	if err != nil {
		return s.Base.Runtime.LoadError(err)
	}

	return s.Base.Runtime.LoadResponse(s.Endpoints)
}

func (s *Runtime) Init(ctx context.Context, req *runtimev0.InitRequest) (*runtimev0.InitResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	var err error
	s.NetworkMappings, err = s.Network(ctx)
	if err != nil {
		return s.Runtime.InitError(err)
	}
	// for docker version
	s.Port = 80

	address := s.NetworkMappings[0].Addresses[0]
	port := strings.Split(address, ":")[1]

	// Run Docker
	s.Runner, err = runners.NewDocker(ctx, runners.WithWorkspace(s.Location))
	if err != nil {
		return s.Runtime.InitError(err)
	}

	s.Runner.WithPort(runners.DockerPort{Container: fmt.Sprintf("%d", s.Port), Host: port})

	envs := []string{
		"FC_ENABLE=1",
		"FC_OUT=out.json",
		"FC_SETTINGS=config/settings",
	}

	s.Runner.WithEnvironmentVariables(envs...)

	cmd := []string{"krakend", "run", "-d", "-c", "config/krakend.tmpl"}
	s.Runner.WithCommand(cmd...)

	err = s.Runner.Init(ctx, image)
	if err != nil {
		return s.Runtime.InitError(err)
	}
	return s.Base.Runtime.InitResponse()
}

func (s *Runtime) Start(ctx context.Context, req *runtimev0.StartRequest) (*runtimev0.StartResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	s.Wool.Debug("starting runtime", wool.NullableField("network mappings", network.MakeNetworkMappingSummary(req.NetworkMappings)))

	// For docker, replace localhost by host.docker.internal
	nm := network.LocalizeMappings(req.NetworkMappings, "host.docker.internal")
	err := s.writeConfig(ctx, nm)
	if err != nil {
		return s.Runtime.StartError(err)
	}

	err = s.Runner.Start(ctx)
	if err != nil {
		return s.Runtime.StartError(err)
	}

	return s.Runtime.StartResponse()
}

func (s *Runtime) Information(ctx context.Context, req *runtimev0.InformationRequest) (*runtimev0.InformationResponse, error) {
	return &runtimev0.InformationResponse{}, nil
}

func (s *Runtime) Stop(ctx context.Context, req *runtimev0.StopRequest) (*runtimev0.StopResponse, error) {
	defer s.Wool.Catch()

	s.Wool.Debug("stopping service")

	err := s.Runner.Stop()
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot stop runner")
	}

	err = s.Base.Stop()
	if err != nil {
		return nil, err
	}
	return &runtimev0.StopResponse{}, nil
}

func (s *Runtime) Communicate(ctx context.Context, req *agentv0.Engage) (*agentv0.InformationRequest, error) {
	return s.Base.Communicate(ctx, req)
}

/* Details

 */

func (s *Runtime) Network(ctx context.Context) ([]*runtimev0.NetworkMapping, error) {
	endpoint := s.Endpoints[0]
	pm, err := network.NewServicePortManager(ctx, s.Identity)
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot create network manager")
	}
	err = pm.Expose(endpoint)
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot add grpc endpoint to network manager")
	}
	err = pm.Reserve(ctx)
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot reserve ports")
	}
	s.Port, err = pm.Port(ctx, endpoint)
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot get port")
	}
	return pm.NetworkMapping(ctx)
}
