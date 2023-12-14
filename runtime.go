package main

import (
	"context"
	"github.com/codefly-dev/core/agents/endpoints"
	"github.com/codefly-dev/core/agents/network"
	"github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/configurations"
	agentsv1 "github.com/codefly-dev/core/generated/v1/go/proto/agents"
	servicev1 "github.com/codefly-dev/core/generated/v1/go/proto/services"
	runtimev1 "github.com/codefly-dev/core/generated/v1/go/proto/services/runtime"
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

func (p *Runtime) Init(ctx context.Context, req *servicev1.InitRequest) (*runtimev1.InitResponse, error) {
	defer p.AgentLogger.Catch()

	err := p.Base.Init(req, p.Settings)
	if err != nil {
		return p.Base.RuntimeInitResponseError(err)
	}

	p.RoutesLocation = p.Local("routing")

	p.Endpoint, err = endpoints.NewRestAPI(&configurations.Endpoint{Name: p.Identity.Name, API: configurations.Rest, Visibility: "public"})
	if err != nil {
		return p.Base.RuntimeInitResponseError(err)
	}

	// From configurations
	err = p.LoadRoutes()
	if err != nil {
		return p.Base.RuntimeInitResponseError(err)
	}
	//channels, err := p.WithCommunications(services.NewDynamicChannel(communicate.Sync))
	//if err != nil {
	//	return p.Base.RuntimeInitResponseError(err)
	//}
	return p.Base.RuntimeInitResponse(p.Endpoints)
}

func (p *Runtime) Configure(ctx context.Context, req *runtimev1.ConfigureRequest) (*runtimev1.ConfigureResponse, error) {
	defer p.AgentLogger.Catch()

	nets, err := p.Network()
	if err != nil {
		return &runtimev1.ConfigureResponse{Status: services.ConfigureError(err)}, nil
	}

	return &runtimev1.ConfigureResponse{Status: services.ConfigureSuccess(),
		NetworkMappings: nets}, nil
}

func (p *Runtime) Start(ctx context.Context, req *runtimev1.StartRequest) (*runtimev1.StartResponse, error) {
	defer p.AgentLogger.Catch()

	p.DebugMe("%s: network mapping: #%d", p.Identity.Name, len(req.NetworkMappings))
	p.DebugMe("%s: routing", p.Routes)

	err := p.writeConfig(req.NetworkMappings)
	if err != nil {
		p.DebugMe("cannot write config: %v", err)
		return nil, p.Wrapf(err, "cannot write config")
	}

	envs := []string{
		"FC_ENABLE=1",
		"FC_OUT=" + p.Local("out.json"),
		"FC_SETTINGS=" + p.Local("config/settings"),
	}

	p.Runner = &runners.Runner{
		Name:          p.Base.Identity.Name,
		Bin:           "krakend",
		Args:          []string{"run", "-d", "-c", p.Local("config/krakend.tmpl")},
		Envs:          envs,
		Dir:           p.Location,
		Debug:         true,
		ServiceLogger: p.ServiceLogger,
		AgentLogger:   p.AgentLogger,
	}

	err = p.Runner.Init(context.Background())
	if err != nil {
		p.DebugMe("runner init failed %v", err)
		return &runtimev1.StartResponse{
			Status: services.StartError(err),
		}, nil
	}
	// useful for debugging -- while I fix the error handling with Start
	tracker, err := p.Runner.Run(context.Background())
	if err != nil {
		p.DebugMe("runner failed %v", err)
		return &runtimev1.StartResponse{
			Status: services.StartError(err),
		}, nil
	}
	p.DebugMe("after run")

	return &runtimev1.StartResponse{
		Status:   services.StartSuccess(),
		Trackers: []*runtimev1.Tracker{tracker.Proto()},
	}, nil
}

func (p *Runtime) Information(ctx context.Context, req *runtimev1.InformationRequest) (*runtimev1.InformationResponse, error) {
	return &runtimev1.InformationResponse{}, nil
}

func (p *Runtime) Stop(ctx context.Context, req *runtimev1.StopRequest) (*runtimev1.StopResponse, error) {
	defer p.AgentLogger.Catch()

	p.AgentLogger.Debugf("stopping service")
	err := p.Runner.Kill()
	if err != nil {
		return nil, shared.Wrapf(err, "cannot kill go")
	}

	err = p.Base.Stop()
	if err != nil {
		return nil, err
	}
	return &runtimev1.StopResponse{}, nil
}

func (p *Runtime) Communicate(ctx context.Context, req *agentsv1.Engage) (*agentsv1.InformationRequest, error) {
	return p.Base.Communicate(ctx, req)
}

/* Details

 */

func (p *Runtime) Network() ([]*runtimev1.NetworkMapping, error) {
	p.DebugMe("in network")
	pm, err := network.NewServicePortManager(p.Context(), p.Identity)
	if err != nil {
		return nil, p.Wrapf(err, "cannot create network manager")
	}
	err = pm.Expose(p.Endpoint)
	if err != nil {
		return nil, p.Wrapf(err, "cannot add grpc endpoint to network manager")
	}
	err = pm.Reserve()
	if err != nil {
		return nil, p.Wrapf(err, "cannot reserve ports")
	}
	p.Port, err = pm.Port(p.Endpoint)
	if err != nil {
		return nil, p.Wrapf(err, "cannot get port")
	}
	return pm.NetworkMapping()
}
