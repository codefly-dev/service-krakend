package main

import (
	"embed"
	"fmt"
	"github.com/codefly-dev/cli/pkg/plugins/communicate"
	"github.com/codefly-dev/cli/pkg/plugins/network"
	"github.com/codefly-dev/cli/pkg/plugins/services"
	v1 "github.com/codefly-dev/cli/pkg/types/v1"
	runtimev1 "github.com/codefly-dev/cli/proto/v1/services/runtime"
	"os"
	"path"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	corev1 "github.com/codefly-dev/cli/proto/v1/core"
	servicev1 "github.com/codefly-dev/cli/proto/v1/services"
	factoryv1 "github.com/codefly-dev/cli/proto/v1/services/factory"
	"github.com/codefly-dev/core/configurations"
)

type Factory struct {
	*Service

	// Create
	create *communicate.ClientContext

	// Sync
	sync                communicate.Client
	seq                 *communicate.Sequence
	syncRoutes          []*configurations.RestRoute
	syncRoutesQuestions []*corev1.Question
}

func NewFactory() *Factory {
	return &Factory{
		Service: NewService(),
	}
}

type Proto struct {
	Package      string
	PackageAlias string
}

type CreateService struct {
	Name      string
	TitleName string
	Proto     Proto
	Go        GenerateInstructions
}

type GenerateInstructions struct {
	Package string
}

type Readme struct {
	Summary string
}

type CreateConfiguration struct {
	Name        string
	Destination string
	Namespace   string
	Domain      string
	Service     CreateService
	Plugin      configurations.Plugin
	Readme      Readme
}

func (p *Factory) Init(req *servicev1.InitRequest) (*factoryv1.InitResponse, error) {
	defer p.PluginLogger.Catch()

	err := p.Base.Init(req, p.Settings)
	if err != nil {
		return nil, err
	}

	p.RoutesLocation = p.Local("routing")

	channels, err := p.WithCommunications(services.NewChannel(communicate.Create, p.create), services.NewDynamicChannel(communicate.Sync))
	if err != nil {
		return nil, err
	}
	return &factoryv1.InitResponse{
		Version:  p.Version(),
		Channels: channels,
	}, nil
}

func (p *Factory) Welcome() (*corev1.Message, map[string]string) {
	return &corev1.Message{Message: `Welcome to the service plugin #(bold,cyan)[go-grc] by plugin #(bold,cyan)[codefly.ai]
Some of the things this plugin provides for you:
- auto-detection of route
- authentication with your authentication provider
- correct CORS configuration
#(bold,cyan)[Coming soon: authorization]
#(bold,cyan)[Coming soon: openAPI output]

Are you excited?`}, map[string]string{
			"PluginName":      plugin.Identifier,
			"PluginPublisher": plugin.Publisher,
		}
}

func (p *Factory) NewCreateCommunicate() (*communicate.ClientContext, error) {
	client, err := communicate.NewClientContext(p.Context(), communicate.Create)
	_, err = client.NewSequence(client.Display(p.Welcome()))
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (p *Factory) Create(req *factoryv1.CreateRequest) (*factoryv1.CreateResponse, error) {
	defer p.PluginLogger.Catch()

	if p.create == nil {
		// Initial setup
		var err error
		p.DebugMe("Setup communication")
		p.create, err = p.NewCreateCommunicate()
		if err != nil {
			return nil, p.PluginLogger.Wrapf(err, "cannot setup up communication")
		}
		err = p.Wire(communicate.Create, p.create)
		if err != nil {
			return nil, p.PluginLogger.Wrapf(err, "cannot wire communication")
		}
		return &factoryv1.CreateResponse{NeedCommunication: true}, nil
	}
	// Make sure the communication for create has been done successfully
	if !p.create.Ready() {
		return nil, p.PluginLogger.Errorf("create: communication not ready")
	}

	create := CreateConfiguration{
		Name:      cases.Title(language.English, cases.NoLower).String(p.Identity.Name),
		Domain:    p.Base.Identity.Domain,
		Namespace: p.Base.Identity.Namespace,
		Readme:    Readme{Summary: p.Base.Identity.Name},
	}

	err := p.Templates(create, services.WithFactory(factory), services.WithBuilder(builder))
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

	return p.Base.Create(p.Settings, p.Endpoint)
}

func (p *Factory) SyncMessage() (*corev1.Message, map[string]string) {
	return &corev1.Message{Message: `Welcome to the service plugin #(bold,cyan)[go-grc] by plugin #(bold,cyan)[codefly.ai]
Some of the things this plugin provides for you:
- gRPC server
- REST server auto-generated (optional)
- hot-reload (optional)
- docker build
- Kubernetes deployment
Are you excited?`}, map[string]string{
			"PluginName":      plugin.Identifier,
			"PluginPublisher": plugin.Publisher,
		}
}

func (p *Factory) Update(req *factoryv1.UpdateRequest) (*factoryv1.UpdateResponse, error) {
	return &factoryv1.UpdateResponse{}, nil
}

func (p *Factory) NewSyncCommunicate(routes []*configurations.RestRoute) error {
	p.DebugMe("adding new routes maybe #%d", len(routes))
	client, err := communicate.NewClientContext(p.Context(), communicate.Sync)
	if err != nil {
		return p.PluginLogger.Wrapf(err, "cannot create new client context")
	}
	// Set the state of sync communicate
	for _, route := range routes {
		p.syncRoutes = append(p.syncRoutes, route)
		fullPath := fmt.Sprintf("%s/%s%s", route.Application, route.Service, route.Path)
		p.syncRoutesQuestions = append(p.syncRoutesQuestions,
			client.NewConfirm(&corev1.Message{
				Name:    fullPath,
				Message: fmt.Sprintf("Want to expose %s: %v?", fullPath, route.Methods),
			}, true))
	}
	questions := []*corev1.Question{client.Display(p.SyncMessage())}
	questions = append(questions, p.syncRoutesQuestions...)
	p.seq, err = client.NewSequence(questions...)
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

func (p *Factory) Sync(req *factoryv1.SyncRequest) (*factoryv1.SyncResponse, error) {
	defer p.PluginLogger.Catch()

	p.DebugMe("known routes: %v", p.Routes)
	if p.sync == nil {
		// From request
		p.DebugMe("Setup communication")

		// Detect if we have unknown routing and create them
		routes := v1.DetectNewRoutes(p.Context(), configurations.UnwrapRoutes(p.Routes), req.DependencyEndpointGroup)

		if len(routes) == 0 {
			p.DebugMe("no new routing detected")
			p.sync = communicate.NewNoOpClientContext()
			return &factoryv1.SyncResponse{}, nil
		}
		err := p.NewSyncCommunicate(routes)
		if err != nil {
			return nil, p.PluginLogger.Wrapf(err, "cannot create sync communicate")
		}
		if p.sync == nil {
			return nil, p.PluginLogger.Errorf("sync: after new sync communicate == nil")
		}
		if len(p.syncRoutesQuestions) > 0 {
			p.DebugMe("we need some communication!")
			return &factoryv1.SyncResponse{NeedCommunication: true}, nil
		} else {
			return &factoryv1.SyncResponse{NeedCommunication: false}, nil
		}
	}
	if p.sync == nil {
		return nil, p.PluginLogger.Errorf("sync: after sync == nil")
	}

	if state := p.sync.(*communicate.ClientContext); state != nil {
		for i := range p.syncRoutesQuestions {
			p.DebugMe("state: %v", state.Get())
			confirm, err := state.SafeConfirm(i + 1) // because of the initial message
			if err != nil {
				return nil, p.PluginLogger.Wrapf(err, "cannot get confirm")
			}
			expose := confirm.Confirmed
			if expose {
				route := p.syncRoutes[i]
				p.DebugMe("exposing %s", route.Path)
				err := route.Save(p.Context(), p.RoutesLocation)
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

	return &factoryv1.SyncResponse{}, nil
}

func (p *Factory) Build(req *factoryv1.BuildRequest) (*factoryv1.BuildResponse, error) {
	defer p.PluginLogger.Catch()

	p.PluginLogger.Debugf("building docker image")

	es := v1.FlattenEndpoints(req.DependencyEndpointGroup)
	net, err := p.Network(es)
	if err != nil {
		return nil, p.PluginLogger.Wrapf(err, "cannot create network")
	}
	p.DebugMe("GOT NET: %v", net)

	return &factoryv1.BuildResponse{}, nil
}

func (p *Factory) Deploy(req *factoryv1.DeploymentRequest) (*factoryv1.DeploymentResponse, error) {
	defer p.PluginLogger.Catch()
	return &factoryv1.DeploymentResponse{}, nil
}

func (p *Factory) LoadEndpoints() error {
	//var err error
	//p.Endpoint, err = endpoints.NewRestApi(&configurations.Endpoint{Name: p.Identity.Name})
	//if err != nil {
	//	return p.Wrapf(err, "cannot  create tcp endpoint")
	//}
	return nil
}

func (p *Factory) Network(endpoints []*corev1.Endpoint) ([]*runtimev1.NetworkMapping, error) {
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
