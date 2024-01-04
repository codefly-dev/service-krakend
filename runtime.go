package main

import (
	"context"
	"fmt"
	"github.com/codefly-dev/core/agents/network"
	"github.com/codefly-dev/core/configurations"
	agentv1 "github.com/codefly-dev/core/generated/go/services/agent/v1"
	runtimev1 "github.com/codefly-dev/core/generated/go/services/runtime/v1"
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

func (s *Runtime) Load(ctx context.Context, req *runtimev1.LoadRequest) (*runtimev1.LoadResponse, error) {
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

func (s *Runtime) Init(ctx context.Context, req *runtimev1.InitRequest) (*runtimev1.InitResponse, error) {
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

	s.Runner.WithPorts(runners.DockerPort{Container: fmt.Sprintf("%d", s.Port), Host: port})

	err = s.Runner.Init(ctx, image)
	if err != nil {
		return s.Runtime.InitError(err)
	}
	return s.Base.Runtime.InitResponse()
}

func (s *Runtime) Start(ctx context.Context, req *runtimev1.StartRequest) (*runtimev1.StartResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	s.Wool.Debug("starting runtime", wool.NullableField("network mappings", network.MakeNetworkMappingSummary(req.NetworkMappings)))

	// For docker, replace localhost by host.docker.internal
	nm := network.LocalizeMappings(req.NetworkMappings, "host.docker.internal")
	err := s.writeConfig(ctx, nm)
	if err != nil {
		return s.Runtime.StartError(err)
	}

	envs := []string{
		"FC_ENABLE=1",
		"FC_OUT=out.json",
		"FC_SETTINGS=config/settings",
	}

	run := runners.NewCommand("krakend", "run", "-d", "-c", "config/krakend.tmpl").WithEnvs(envs)

	err = s.Runner.Start(ctx, run)
	if err != nil {
		return s.Runtime.StartError(err)
	}

	return s.Runtime.StartResponse()
}

func (s *Runtime) Information(ctx context.Context, req *runtimev1.InformationRequest) (*runtimev1.InformationResponse, error) {
	return &runtimev1.InformationResponse{}, nil
}

func (s *Runtime) Stop(ctx context.Context, req *runtimev1.StopRequest) (*runtimev1.StopResponse, error) {
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
	return &runtimev1.StopResponse{}, nil
}

func (s *Runtime) Communicate(ctx context.Context, req *agentv1.Engage) (*agentv1.InformationRequest, error) {
	return s.Base.Communicate(ctx, req)
}

/* Details

 */

func (s *Runtime) Network(ctx context.Context) ([]*runtimev1.NetworkMapping, error) {
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
