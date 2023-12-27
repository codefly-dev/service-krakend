package main

import (
	"context"
	"github.com/codefly-dev/core/configurations/standards"

	"github.com/codefly-dev/core/agents/network"
	"github.com/codefly-dev/core/configurations"
	agentv1 "github.com/codefly-dev/core/generated/go/services/agent/v1"
	runtimev1 "github.com/codefly-dev/core/generated/go/services/runtime/v1"
	"github.com/codefly-dev/core/runners"
)

type Runtime struct {
	*Service

	// internal
	Runner *runners.Runner
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

	err := s.Base.Load(ctx, req.Identity, s.Settings)
	if err != nil {
		return s.Base.Runtime.LoadError(err)
	}

	s.RoutesLocation = s.Local("routing")

	s.Endpoint, err = configurations.NewRestAPI(ctx, &configurations.Endpoint{Name: s.Identity.Name, API: standards.REST, Visibility: "public"})
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

	var err error
	s.NetworkMappings, err = s.Network(ctx)
	if err != nil {
		return s.Base.Runtime.InitError(err)
	}

	return s.Base.Runtime.InitResponse()
}

func (s *Runtime) Start(ctx context.Context, req *runtimev1.StartRequest) (*runtimev1.StartResponse, error) {
	defer s.Wool.Catch()

	err := s.writeConfig(ctx, req.NetworkMappings)
	if err != nil {
		return s.Runtime.StartError(err)
	}

	envs := []string{
		"FC_ENABLE=1",
		"FC_OUT=" + s.Local("out.json"),
		"FC_SETTINGS=" + s.Local("config/settings"),
	}

	s.Runner = &runners.Runner{
		Name:  s.Base.Identity.Name,
		Bin:   "krakend",
		Args:  []string{"run", "-d", "-c", s.Local("config/krakend.tmpl")},
		Envs:  envs,
		Dir:   s.Location,
		Debug: true,
	}

	_, err = s.Runner.Run(context.Background())
	if err != nil {
		return s.Runtime.StartError(err)
	}

	return s.Runtime.StartResponse(nil)
}

func (s *Runtime) Information(ctx context.Context, req *runtimev1.InformationRequest) (*runtimev1.InformationResponse, error) {
	return &runtimev1.InformationResponse{}, nil
}

func (s *Runtime) Stop(ctx context.Context, req *runtimev1.StopRequest) (*runtimev1.StopResponse, error) {
	defer s.Wool.Catch()

	s.Wool.Debug("stopping service")
	//err := s.Runner.Kill(ctx)
	//if err != nil {
	//	return nil, s.Wool.Wrapf(err, "cannot kill go")
	//}

	err := s.Base.Stop()
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
	pm, err := network.NewServicePortManager(ctx, s.Identity)
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot create network manager")
	}
	err = pm.Expose(s.Endpoint)
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot add grpc endpoint to network manager")
	}
	err = pm.Reserve(ctx)
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot reserve ports")
	}
	s.Port, err = pm.Port(ctx, s.Endpoint)
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot get port")
	}
	return pm.NetworkMapping(ctx)
}
