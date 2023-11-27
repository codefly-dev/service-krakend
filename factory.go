package main

import (
	"embed"
	"fmt"
	"github.com/codefly-dev/core/agents/communicate"
	"github.com/codefly-dev/core/agents/endpoints"
	"github.com/codefly-dev/core/agents/network"
	"github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/configurations"
	agentsv1 "github.com/codefly-dev/core/proto/v1/go/agents"
	basev1 "github.com/codefly-dev/core/proto/v1/go/base"
	servicev1 "github.com/codefly-dev/core/proto/v1/go/services"
	factoryv1 "github.com/codefly-dev/core/proto/v1/go/services/factory"
	runtimev1 "github.com/codefly-dev/core/proto/v1/go/services/runtime"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"
	"os"
	"path"
)

type Factory struct {
	*Service

	// Create
	create *communicate.ClientContext

	// Sync
	sync                communicate.Client
	seq                 *communicate.Sequence
	syncRoutes          []*configurations.RestRoute
	syncRoutesQuestions []*agentsv1.Question
}

func NewFactory() *Factory {
	return &Factory{
		Service: NewService(),
	}
}

func (p *Factory) Init(req *servicev1.InitRequest) (*factoryv1.InitResponse, error) {
	defer p.AgentLogger.Catch()

	err := p.Base.Init(req, p.Settings)
	if err != nil {
		return nil, err
	}

	p.RoutesLocation = p.Local("routing")

	channels, err := p.WithCommunications(services.NewChannel(communicate.Create, p.create), services.NewDynamicChannel(communicate.Sync))
	if err != nil {
		return nil, err
	}

	err = p.LoadEndpoints()
	if err != nil {
		return p.FactoryInitResponseError(err)
	}

	readme, err := templates.ApplyTemplateFrom(shared.Embed(factory), "templates/factory/README.md", p.Information)
	if err != nil {
		return nil, err
	}

	return &factoryv1.InitResponse{
		Version:   p.Version(),
		Endpoints: p.Endpoints,
		Channels:  channels,
		ReadMe:    readme,
	}, nil
}

const Watch = "watch"
const WithRest = "with_rest"

func (p *Factory) Create(req *factoryv1.CreateRequest) (*factoryv1.CreateResponse, error) {
	defer p.AgentLogger.Catch()

	err := p.Templates(p.Information, services.WithFactory(factory), services.WithBuilder(builder))
	if err != nil {
		return nil, err
	}

	err = os.MkdirAll(path.Join(p.Location, "routing"), 0755)
	if err != nil {
		return nil, p.Wrapf(err, "cannot create routing directory")
	}
	err = p.LoadEndpoints()
	if err != nil {
		return nil, p.Wrapf(err, "cannot create endpoints")
	}

	return p.Base.Create(p.Settings, p.Endpoints...)
}

func (p *Factory) Update(req *factoryv1.UpdateRequest) (*factoryv1.UpdateResponse, error) {
	defer p.AgentLogger.Catch()

	return &factoryv1.UpdateResponse{}, nil
}

func (p *Factory) Sync(req *factoryv1.SyncRequest) (*factoryv1.SyncResponse, error) {
	defer p.AgentLogger.Catch()

	p.DebugMe("known routes: %v", p.Routes)
	if p.sync == nil {
		// From request
		p.DebugMe("Setup communication")

		// Detect if we have unknown routing and create them
		routes := endpoints.DetectNewRoutes(p.Context(), configurations.UnwrapRoutes(p.Routes), req.DependencyEndpointGroup)

		if len(routes) == 0 {
			p.DebugMe("no new routing detected")
			p.sync = communicate.NewNoOpClientContext()
			return &factoryv1.SyncResponse{}, nil
		}
		err := p.NewSyncCommunicate(routes)
		if err != nil {
			return nil, p.Wrapf(err, "cannot create sync communicate")
		}
		if p.sync == nil {
			return nil, p.Errorf("sync: after new sync communicate == nil")
		}
		if len(p.syncRoutesQuestions) > 0 {
			p.DebugMe("we need some communication!")
			return &factoryv1.SyncResponse{NeedCommunication: true}, nil
		} else {
			return &factoryv1.SyncResponse{NeedCommunication: false}, nil
		}
	}
	if p.sync == nil {
		return nil, p.Errorf("sync: after sync == nil")
	}

	if state := p.sync.(*communicate.ClientContext); state != nil {
		for i := range p.syncRoutesQuestions {
			p.DebugMe("state: %v", state.Get())
			confirm, err := state.SafeConfirm(i)
			if err != nil {
				return nil, p.Wrapf(err, "cannot get confirm")
			}
			expose := confirm.Confirmed
			if expose {
				route := p.syncRoutes[i]
				p.DebugMe("exposing %s", route.Path)
				err := route.Save(p.Context(), p.RoutesLocation)
				if err != nil {
					return nil, p.Wrapf(err, "cannot save route")
				}
			}
		}
	}

	// Make sure the communication for create has been done successfully
	if !p.sync.Ready() {
		return nil, p.Errorf("sync: validation communication not ready")
	}

	return &factoryv1.SyncResponse{}, nil
}

type Env struct {
	Key   string
	Value string
}

type DockerTemplating struct {
	Envs []Env
}

func (p *Factory) Build(req *factoryv1.BuildRequest) (*factoryv1.BuildResponse, error) {
	p.AgentLogger.Debugf("building docker image")

	return &factoryv1.BuildResponse{}, nil
}

func (p *Factory) Deploy(req *factoryv1.DeploymentRequest) (*factoryv1.DeploymentResponse, error) {
	return &factoryv1.DeploymentResponse{}, nil
}

func (p *Factory) Network(endpoints []*basev1.Endpoint) ([]*runtimev1.NetworkMapping, error) {
	p.DebugMe("in network")
	pm, err := network.NewServiceDnsManager(p.Context(), p.Identity)
	if err != nil {
		return nil, p.Wrapf(err, "cannot create network manager")
	}
	for _, endpoint := range endpoints {
		err = pm.Expose(endpoint)
		if err != nil {
			return nil, p.Wrapf(err, "cannot add grpc endpoint to network manager")
		}

	}

	err = pm.Reserve()
	if err != nil {
		return nil, p.Wrapf(err, "cannot reserve ports")
	}
	return pm.NetworkMapping()
}

//go:embed templates/factory
var factory embed.FS

//go:embed templates/builder
var builder embed.FS

func (p *Factory) NewSyncCommunicate(routes []*configurations.RestRoute) error {
	p.DebugMe("adding new routes maybe #%d", len(routes))
	client, err := communicate.NewClientContext(p.Context(), communicate.Sync)
	if err != nil {
		return p.Wrapf(err, "cannot create new client context")
	}
	// Set the state of sync communicate
	for _, route := range routes {
		p.syncRoutes = append(p.syncRoutes, route)
		fullPath := fmt.Sprintf("%s/%s%s", route.Application, route.Service, route.Path)
		p.syncRoutesQuestions = append(p.syncRoutesQuestions,
			client.NewConfirm(&agentsv1.Message{
				Name:    fullPath,
				Message: fmt.Sprintf("Want to expose %s: %v?", fullPath, route.Methods),
			}, true))
	}
	p.seq, err = client.NewSequence(p.syncRoutesQuestions...)
	if err != nil {
		return p.Wrapf(err, "can't create sequence")
	}
	p.sync = client
	err = p.Wire(communicate.Sync, client)
	if err != nil {
		return p.Wrapf(err, "cannot wire")
	}
	return nil
}
