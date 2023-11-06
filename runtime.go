package main

import (
	"context"
	"fmt"
	"path"

	"github.com/codefly-dev/cli/pkg/plugins/communicate"
	"github.com/codefly-dev/cli/pkg/plugins/services"
	"github.com/codefly-dev/cli/pkg/runners"
	corev1 "github.com/codefly-dev/cli/proto/v1/core"
	servicev1 "github.com/codefly-dev/cli/proto/v1/services"
	runtimev1 "github.com/codefly-dev/cli/proto/v1/services/runtime"
	"github.com/codefly-dev/core/configurations"
)

type Runtime struct {
	*Service

	// State
	Routes         []*configurations.RestRoute
	RoutesLocation string

	// internal
	Runner *runners.Runner

	// TODO: Get is to work first, then refactor
	sync                communicate.Client
	syncRoutes          []*configurations.RestRoute
	syncRoutesQuestions []*corev1.Question
}

func NewRuntime() *Runtime {
	service := NewService()
	return &Runtime{
		Service: service,
	}
}

func (p *Runtime) Init(req *servicev1.InitRequest) (*runtimev1.InitResponse, error) {
	defer p.PluginLogger.Catch()

	err := p.Base.Init(req, &p.Spec)
	if err != nil {
		return nil, err
	}

	// From configuration
	err = p.LoadRoutes()
	if err != nil {
		return nil, p.PluginLogger.Wrapf(err, "cannot load routes")
	}

	channels, err := p.WithCommunications(services.NewDynamicChannel(communicate.Sync))
	if err != nil {
		return nil, err
	}

	return &runtimev1.InitResponse{
		Version:  p.Base.Version(),
		Channels: channels,
	}, nil
}

func (p *Runtime) Configure(req *runtimev1.ConfigureRequest) (*runtimev1.ConfigureResponse, error) {
	defer p.PluginLogger.Catch()

	p.PluginLogger.Info("%s -> configure", p.Identity.Name)

	return &runtimev1.ConfigureResponse{Status: services.ConfigureSuccess()}, nil
}

func (p *Runtime) Start(req *runtimev1.StartRequest) (*runtimev1.StartResponse, error) {
	defer p.PluginLogger.Catch()

	p.PluginLogger.DebugMe("%s: network mapping: %v", p.Identity.Name, req.NetworkMappings)
	p.PluginLogger.DebugMe("%s: routes", p.Routes)

	envs := []string{
		"FC_ENABLE=1",
		"FC_OUT=/etc/krakend/out.json",
		"FC_PARTIALS=config/partials",
		"FC_SETTINGS=config/settings/local",
		"FC_TEMPLATES=config/templates",
	}

	p.Runner = &runners.Runner{
		Name:          p.Base.Identity.Name,
		Bin:           "krakend",
		Args:          []string{"run", "--config", "krakend.config"},
		Envs:          envs,
		Dir:           p.Location,
		Debug:         true,
		ServiceLogger: p.ServiceLogger,
		PluginLogger:  p.PluginLogger,
		Cmd:           nil,
	}

	err := p.Runner.Init(context.Background())
	if err != nil {
		return &runtimev1.StartResponse{
			Status: services.StartError(err),
		}, nil
	}

	tracker, err := p.Runner.Run(context.Background())
	if err != nil {
		return &runtimev1.StartResponse{
			Status: services.StartError(err),
		}, nil
	}

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

func (p *Runtime) NewSyncCommunicate(routes []*configurations.RestRoute) error {
	if len(routes) == 0 {
		p.PluginLogger.Info("no new routes detected")
		p.sync = communicate.NewNoOpClientContext()
		return nil
	}
	client := communicate.NewClientContext(communicate.Sync, p.PluginLogger)
	// Set the state of sync communicate
	for _, route := range routes {
		p.syncRoutes = append(p.syncRoutes, route)
		p.syncRoutesQuestions = append(p.syncRoutesQuestions,
			client.NewConfirm(&corev1.Message{
				Name:    route.Path,
				Message: fmt.Sprintf("Want to expose %s: %v?", route.Path, route.Methods),
			}, true))
	}
	err := client.NewSequence(
		p.syncRoutesQuestions...,
	)
	if err != nil {
		return p.PluginLogger.Wrapf(err, "can't create sequence")
	}
	p.sync = client
	err = p.Wire(communicate.Sync, client)
	if err != nil {
		return p.PluginLogger.Wrapf(err, "cannot wire")
	}
	return nil
}

// LoadRoutes from routes configuration folder
func (p *Runtime) LoadRoutes() error {
	p.RoutesLocation = path.Join(p.Location, "codefly/routes")
	var err error
	p.Routes, err = services.LoadApplicationRoutes(p.RoutesLocation, p.PluginLogger)
	if err != nil {
		return p.PluginLogger.Wrapf(err, "cannot load routes")
	}
	return nil
}

func (p *Runtime) Sync(req *runtimev1.SyncRequest) (*runtimev1.SyncResponse, error) {
	defer p.PluginLogger.Catch()

	// This is the first call
	if p.sync == nil {
		// From request
		p.PluginLogger.DebugMe("first call to sync")
		routes := services.ConvertApplicationRoutes(req.Routes)
		p.PluginLogger.DebugMe("received from request routes: %v", routes)

		p.PluginLogger.DebugMe("loaded from configuration routes: %v", p.Routes)

		// Detect if we have unknown routes
		routes = services.DetectNewRoutes(p.Routes, routes)
		p.PluginLogger.DebugMe("new routes detected: %v", routes)

		err := p.NewSyncCommunicate(routes)
		if err != nil {
			return nil, p.PluginLogger.Wrapf(err, "cannot create sync communicate")
		}
		if p.sync == nil {
			return nil, p.PluginLogger.Errorf("sync: after new sync communicate == nil")
		}
		if len(p.syncRoutesQuestions) > 0 {
			p.PluginLogger.DebugMe("we need some communication!")
			return &runtimev1.SyncResponse{NeedCommunication: true}, nil
		} else {
			return &runtimev1.SyncResponse{NeedCommunication: false}, nil
		}
	}
	if p.sync == nil {
		return nil, p.PluginLogger.Errorf("sync: after sync == nil")
	}

	if state := p.sync.(*communicate.ClientContext); state != nil {
		for i := range p.syncRoutesQuestions {
			expose := state.Confirm(i).Confirmed
			if expose {
				route := p.syncRoutes[i]
				p.PluginLogger.DebugMe("exposing %s", route.Path)
				err := route.Save(p.RoutesLocation, p.PluginLogger)
				if err != nil {
					return nil, p.PluginLogger.Wrapf(err, "cannot save route")
				}
			}
		}
	}

	// Make sure the communication for create has been done successfully
	if !p.sync.Ready() {
		return nil, p.PluginLogger.Errorf("sync: validation communication not ready")
	}

	return &runtimev1.SyncResponse{}, nil
}

func (p *Runtime) Build(req *runtimev1.BuildRequest) (*runtimev1.BuildResponse, error) {
	defer p.PluginLogger.Catch()

	p.PluginLogger.Debugf("building docker image")

	return &runtimev1.BuildResponse{}, nil
}

func (p *Runtime) Deploy(req *runtimev1.DeploymentRequest) (*runtimev1.DeploymentResponse, error) {
	defer p.PluginLogger.Catch()
	return &runtimev1.DeploymentResponse{}, nil
}

func (p *Runtime) Communicate(req *corev1.Engage) (*corev1.InformationRequest, error) {
	p.PluginLogger.DebugMe("factory communicate: %v", req)
	return p.Base.Communicate(req)
}

/* Details

 */
