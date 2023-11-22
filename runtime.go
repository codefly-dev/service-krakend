package main

import (
	"context"
	"github.com/codefly-dev/cli/pkg/plugins/communicate"
	"github.com/codefly-dev/cli/pkg/plugins/endpoints"
	"github.com/codefly-dev/cli/pkg/plugins/network"
	"github.com/codefly-dev/cli/pkg/plugins/services"
	"github.com/codefly-dev/cli/pkg/runners"
	corev1 "github.com/codefly-dev/cli/proto/v1/core"
	servicev1 "github.com/codefly-dev/cli/proto/v1/services"
	runtimev1 "github.com/codefly-dev/cli/proto/v1/services/runtime"
	"github.com/codefly-dev/core/configurations"
)

type Runtime struct {
	*Service

	Port int

	// internal
	Runner *runners.Runner
}

func NewRuntime() *Runtime {
	service := NewService()
	return &Runtime{
		Service: service,
	}
}

type Auth struct {
	Protected bool `yaml:"protected"`
}

// RestRoute extends the concept of RestRoute to add Auth aspects
type RestRoute = configurations.ExtendedRestRoute[Auth]

func (p *Runtime) Init(req *servicev1.InitRequest) (*runtimev1.InitResponse, error) {
	defer p.PluginLogger.Catch()

	err := p.Base.Init(req, p.Settings)
	if err != nil {
		return p.Base.RuntimeInitResponseError(err)
	}

	p.Endpoint, err = endpoints.NewRestApi(&configurations.Endpoint{Name: p.Identity.Name, Api: configurations.Rest, Scope: "public"})
	if err != nil {
		return p.Base.RuntimeInitResponseError(err)
	}

	// From configurations
	err = p.LoadRoutes()
	if err != nil {
		return p.Base.RuntimeInitResponseError(err)
	}
	channels, err := p.WithCommunications(services.NewDynamicChannel(communicate.Sync))
	if err != nil {
		return p.Base.RuntimeInitResponseError(err)
	}
	return p.Base.RuntimeInitResponse(p.Endpoints, channels...)
}

func (p *Runtime) Configure(req *runtimev1.ConfigureRequest) (*runtimev1.ConfigureResponse, error) {
	defer p.PluginLogger.Catch()

	nets, err := p.Network()
	if err != nil {
		return &runtimev1.ConfigureResponse{Status: services.ConfigureError(err)}, nil
	}

	return &runtimev1.ConfigureResponse{Status: services.ConfigureSuccess(),
		NetworkMappings: nets}, nil
}

func (p *Runtime) Start(req *runtimev1.StartRequest) (*runtimev1.StartResponse, error) {
	defer p.PluginLogger.Catch()

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
		Args:          []string{"run", "-d", "--config", p.Local("config/krakend.tmpl")},
		Envs:          envs,
		Dir:           p.Location,
		Debug:         true,
		ServiceLogger: p.ServiceLogger,
		PluginLogger:  p.PluginLogger,
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

func (p *Runtime) Information(req *runtimev1.InformationRequest) (*runtimev1.InformationResponse, error) {
	return &runtimev1.InformationResponse{}, nil
}

func (p *Runtime) Stop(req *runtimev1.StopRequest) (*runtimev1.StopResponse, error) {
	defer p.PluginLogger.Catch()

	p.PluginLogger.Debugf("%s: stopping service", p.Base.Identity.Name)

	err := p.Base.Stop()
	if err != nil {
		return nil, err
	}
	return &runtimev1.StopResponse{}, nil
}

func (p *Runtime) Communicate(req *corev1.Engage) (*corev1.InformationRequest, error) {
	p.DebugMe("factory communicate: %v", req)
	return p.Base.Communicate(req)
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
