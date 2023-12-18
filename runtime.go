package main

import (
	"context"

	"github.com/codefly-dev/core/agents/endpoints"
	"github.com/codefly-dev/core/agents/network"
	"github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/configurations"
	agentv1 "github.com/codefly-dev/core/generated/go/services/agent/v1"
	runtimev1 "github.com/codefly-dev/core/generated/go/services/runtime/v1"
	"github.com/codefly-dev/core/runners"
	"github.com/codefly-dev/core/shared"
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

func (s *Runtime) Init(ctx context.Context, req *runtimev1.InitRequest) (*runtimev1.InitResponse, error) {
	defer s.Wool.Catch()

	err := s.Base.Init(ctx, req.Identity, s.Settings)
	if err != nil {
		return s.Base.RuntimeInitResponseError(err)
	}

	s.RoutesLocation = s.Local("routing")

	s.Endpoint, err = endpoints.NewRestAPI(ctx, &configurations.Endpoint{Name: s.Identity.Name, API: configurations.Rest, Visibility: "public"})
	if err != nil {
		return s.Base.RuntimeInitResponseError(err)
	}

	// From configurations
	err = s.LoadRoutes(ctx)
	if err != nil {
		return s.Base.RuntimeInitResponseError(err)
	}
	//channels, err := s.WithCommunications(services.NewDynamicChannel(communicate.Sync))
	//if err != nil {
	//	return s.Base.RuntimeInitResponseError(err)
	//}
	return s.Base.RuntimeInitResponse(s.Endpoints)
}

func (s *Runtime) Configure(ctx context.Context, req *runtimev1.ConfigureRequest) (*runtimev1.ConfigureResponse, error) {
	defer s.Wool.Catch()

	nets, err := s.Network(ctx)
	if err != nil {
		return &runtimev1.ConfigureResponse{Status: services.ConfigureError(err)}, nil
	}

	return &runtimev1.ConfigureResponse{Status: services.ConfigureSuccess(),
		NetworkMappings: nets}, nil
}

func (s *Runtime) Start(ctx context.Context, req *runtimev1.StartRequest) (*runtimev1.StartResponse, error) {
	defer s.Wool.Catch()

	s.DebugMe("%s: network mapping: #%d", s.Identity.Name, len(req.NetworkMappings))
	s.DebugMe("%s: routing", s.Routes)

	err := s.writeConfig(ctx, req.NetworkMappings)
	if err != nil {
		s.DebugMe("cannot write config: %v", err)
		return nil, s.Wrapf(err, "cannot write config")
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

	err = s.Runner.Init(context.Background())
	if err != nil {
		s.DebugMe("runner init failed %v", err)
		return &runtimev1.StartResponse{
			Status: services.StartError(err),
		}, nil
	}
	// useful for debugging -- while I fix the error handling with Start
	tracker, err := s.Runner.Run(context.Background())
	if err != nil {
		s.DebugMe("runner failed %v", err)
		return &runtimev1.StartResponse{
			Status: services.StartError(err),
		}, nil
	}
	s.DebugMe("after run")

	return &runtimev1.StartResponse{
		Status:   services.StartSuccess(),
		Trackers: []*runtimev1.Tracker{tracker.Proto()},
	}, nil
}

func (s *Runtime) Information(ctx context.Context, req *runtimev1.InformationRequest) (*runtimev1.InformationResponse, error) {
	return &runtimev1.InformationResponse{}, nil
}

func (s *Runtime) Stop(ctx context.Context, req *runtimev1.StopRequest) (*runtimev1.StopResponse, error) {
	defer s.Wool.Catch()

	s.Wool.Debug("stopping service")
	err := s.Runner.Kill()
	if err != nil {
		return nil, shared.Wrapf(err, "cannot kill go")
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
	s.DebugMe("in network")
	pm, err := network.NewServicePortManager(ctx, s.Identity)
	if err != nil {
		return nil, s.Wrapf(err, "cannot create network manager")
	}
	err = pm.Expose(s.Endpoint)
	if err != nil {
		return nil, s.Wrapf(err, "cannot add grpc endpoint to network manager")
	}
	err = pm.Reserve()
	if err != nil {
		return nil, s.Wrapf(err, "cannot reserve ports")
	}
	s.Port, err = pm.Port(s.Endpoint)
	if err != nil {
		return nil, s.Wrapf(err, "cannot get port")
	}
	return pm.NetworkMapping()
}
