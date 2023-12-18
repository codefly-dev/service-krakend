package main

import (
	"context"
	"embed"
	"os"

	"github.com/codefly-dev/core/agents/endpoints"
	dockerhelpers "github.com/codefly-dev/core/agents/helpers/docker"
	"github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/configurations"
	basev1 "github.com/codefly-dev/core/generated/go/base/v1"
	agentv1 "github.com/codefly-dev/core/generated/go/services/agent/v1"
	factoryv1 "github.com/codefly-dev/core/generated/go/services/factory/v1"
	runtimev1 "github.com/codefly-dev/core/generated/go/services/runtime/v1"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"
)

type Factory struct {
	*Service

	syncRoutes          []*configurations.RestRoute
	syncRoutesQuestions []*agentv1.Question
}

func NewFactory() *Factory {
	return &Factory{
		Service: NewService(),
	}
}

func (s *Factory) Init(ctx context.Context, req *factoryv1.InitRequest) (*factoryv1.InitResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Provider.WithContext(ctx)

	err := s.Base.Init(ctx, req.Identity, s.Settings)
	if err != nil {
		return nil, err
	}

	s.RoutesLocation = s.Local("routing")
	err = shared.CheckDirectoryOrCreate(ctx, s.RoutesLocation)
	if err != nil {
		return nil, s.Wrapf(err, "cannot create routes location")
	}

	err = s.LoadRoutes(ctx)
	if err != nil {
		return nil, s.Wrapf(err, "cannot load routes")
	}

	err = s.LoadEndpoints(ctx)
	if err != nil {
		return s.FactoryInitResponseError(err)
	}

	readme, err := templates.ApplyTemplateFrom(shared.Embed(factory), "templates/factory/README.md", s.Information)
	if err != nil {
		return nil, err
	}

	return s.FactoryInitResponse(s.Endpoints, readme)
}

func (s *Factory) Create(ctx context.Context, req *factoryv1.CreateRequest) (*factoryv1.CreateResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Provider.WithContext(ctx)

	err := s.Templates(ctx, s.Information, services.WithFactory(factory), services.WithBuilder(builder))
	if err != nil {
		return nil, err
	}

	err = s.LoadEndpoints(ctx)
	if err != nil {
		return nil, s.Wrapf(err, "cannot create endpoints")
	}

	return s.Base.CreateResponse(ctx, s.Settings, s.Endpoints...)
}

func (s *Factory) Update(ctx context.Context, req *factoryv1.UpdateRequest) (*factoryv1.UpdateResponse, error) {
	defer s.Wool.Catch()

	return &factoryv1.UpdateResponse{}, nil
}

func (s *Factory) CheckState(ctx context.Context, group *basev1.EndpointGroup) error {
	defer s.Wool.Catch()
	es := endpoints.FlattenEndpoints(ctx, group)
	// routes should correspond to dependency groups
	for _, route := range s.Routes {
		matchingEndpoint := endpoints.FindEndpointForRoute(ctx, es, configurations.UnwrapRoute(route))
		if matchingEndpoint == nil {
			s.DebugMe("found a route not matching to a endpoint - deleting it")
		}
		err := route.Delete(ctx, s.RoutesLocation)
		if err != nil {
			return s.Wrapf(err, "cannot delete route")
		}
	}
	return nil
}

func (s *Factory) Sync(ctx context.Context, req *factoryv1.SyncRequest) (*factoryv1.SyncResponse, error) {
	defer s.Wool.Catch()

	err := s.CheckState(ctx, req.DependencyEndpointGroup)
	if err != nil {
		return nil, s.Wrapf(err, "cannot check state")
	}
	//
	//if s.sync == nil {
	//	// From request
	//	s.DebugMe("Setup communication")
	//
	//	// Detect if we have unknown routing and create them
	//	routes := endpoints.DetectNewRoutes(ctx, configurations.UnwrapRoutes(s.Routes), req.DependencyEndpointGroup)
	//
	//	if len(routes) == 0 {
	//		s.DebugMe("no new routing detected")
	//		s.sync = communicate.NewNoOpClientContext()
	//		return &factoryv1.SyncResponse{}, nil
	//	}
	//	err := s.NewSyncCommunicate(routes)
	//	if err != nil {
	//		return nil, s.Wrapf(err, "cannot create sync communicate")
	//	}
	//	if s.sync == nil {
	//		return nil, s.Errorf("sync: after new sync communicate == nil")
	//	}
	//	if len(s.syncRoutesQuestions) > 0 {
	//		s.DebugMe("we need some communication!")
	//		return &factoryv1.SyncResponse{NeedCommunication: true}, nil
	//	} else {
	//		return &factoryv1.SyncResponse{NeedCommunication: false}, nil
	//	}
	//}
	//if s.sync == nil {
	//	return nil, s.Errorf("sync: after sync == nil")
	//}
	//
	//if state := s.sync.(*communicate.ClientContext); state != nil {
	//	for i := range s.syncRoutesQuestions {
	//		s.DebugMe("state: %v", state.Get())
	//		confirm, err := state.SafeConfirm(i)
	//		if err != nil {
	//			return nil, s.Wrapf(err, "cannot get confirm")
	//		}
	//		expose := confirm.Confirmed
	//		if expose {
	//			route := s.syncRoutes[i]
	//			s.DebugMe("exposing %s", route.Path)
	//			err := route.Save(ctx, s.RoutesLocation)
	//			if err != nil {
	//				return nil, s.Wrapf(err, "cannot save route")
	//			}
	//		}
	//	}
	//}
	//
	//// Make sure the communication for create has been done successfully
	//if !s.sync.Ready() {
	//	return nil, s.Errorf("sync: validation communication not ready")
	//}

	return &factoryv1.SyncResponse{}, nil
}

type Env struct {
	Key   string
	Value string
}

type DockerTemplating struct {
	Envs []Env
}

func (s *Factory) Build(ctx context.Context, req *factoryv1.BuildRequest) (*factoryv1.BuildResponse, error) {
	s.Wool.Debug("building docker image")
	// We want to use DNS to create NetworkMapping
	networkMapping, err := s.Network(endpoints.FlattenEndpoints(ctx, req.DependencyEndpointGroup))
	if err != nil {
		return nil, s.Wrapf(err, "cannot create network mapping")
	}
	config, err := s.createConfig(ctx, networkMapping)
	if err != nil {
		return nil, s.Wrapf(err, "cannot write config")
	}

	target := s.Local("codefly/builder/settings/routing.json")
	err = os.WriteFile(target, config, 0o644)
	if err != nil {
		return nil, s.Wrapf(err, "cannot write settings to %s", target)
	}

	err = os.Remove(s.Local("codefly/builder/Dockerfile"))
	if err != nil {
		return nil, s.Wrapf(err, "cannot remove dockerfile")
	}
	err = s.Templates(nil, services.WithBuilder(builder))
	if err != nil {
		return nil, s.Wrapf(err, "cannot copy and apply template")
	}
	builder, err := dockerhelpers.NewBuilder(dockerhelpers.BuilderConfiguration{
		Root:       s.Location,
		Dockerfile: "codefly/builder/Dockerfile",
		Image:      s.DockerImage().Name,
		Tag:        s.DockerImage().Tag,
	})
	if err != nil {
		return nil, s.Wrapf(err, "cannot create builder")
	}
	// builder.WithLogger(s.Wool)
	_, err = builder.Build()
	if err != nil {
		return nil, s.Wrapf(err, "cannot build image")
	}
	return &factoryv1.BuildResponse{}, nil
}

type Deployment struct {
	Replicas int
}

type DeploymentParameter struct {
	Image *configurations.DockerImage
	*services.Information
	Deployment
}

func (s *Factory) Deploy(ctx context.Context, req *factoryv1.DeploymentRequest) (*factoryv1.DeploymentResponse, error) {
	defer s.Wool.Catch()
	//deploy := DeploymentParameter{Image: s.DockerImage(), Information: s.Information, Deployment: Deployment{Replicas: 1}}
	//err := s.Templates(deploy,
	//	services.WithDeploymentFor(deployment, "kustomize/base", templates.WithOverrideAll()),
	//	services.WithDeploymentFor(deployment, "kustomize/overlays/environment",
	//		services.WithDestination("kustomize/overlays/%s", req.Environment.Name), templates.WithOverrideAll()),
	//)
	//if err != nil {
	//	return nil, err
	//}
	return &factoryv1.DeploymentResponse{}, nil
}

func (s *Factory) Network(es []*basev1.Endpoint) ([]*runtimev1.NetworkMapping, error) {
	return nil, nil
	//s.DebugMe("in network: %v", endpoints.Condensed(es))
	//pm, err := network.NewServiceDnsManager(ctx, s.Identity)
	//if err != nil {
	//	return nil, s.Wrapf(err, "cannot create network manager")
	//}
	//for _, endpoint := range es {
	//	err = pm.Expose(endpoint)
	//	if err != nil {
	//		return nil, s.Wrapf(err, "cannot add grpc endpoint to network manager")
	//	}
	//}
	//err = pm.Reserve()
	//if err != nil {
	//	return nil, s.Wrapf(err, "cannot reserve ports")
	//}
	//return pm.NetworkMapping()
}

func (s *Factory) NewSyncCommunicate(routes []*configurations.RestRoute) error {
	//s.DebugMe("adding new routes maybe #%d", len(routes))
	//client, err := communicate.NewClientContext(ctx, communicate.Sync)
	//if err != nil {
	//	return s.Wrapf(err, "cannot create new client context")
	//}
	//// Set the state of sync communicate
	//for _, route := range routes {
	//	s.syncRoutes = append(s.syncRoutes, route)
	//	fullPath := fmt.Sprintf("%s/%s%s", route.Application, route.Service, route.Path)
	//	s.syncRoutesQuestions = append(s.syncRoutesQuestions,
	//		client.NewConfirm(&agentsv1.Message{
	//			Name:    fullPath,
	//			Message: fmt.Sprintf("Want to expose %s: %v?", fullPath, route.Methods),
	//		}, true))
	//}
	//s.seq, err = client.NewSequence(s.syncRoutesQuestions...)
	//if err != nil {
	//	return s.Wrapf(err, "can't create sequence")
	//}
	//s.sync = client
	//err = s.Wire(communicate.Sync, client)
	//if err != nil {
	//	return s.Wrapf(err, "cannot wire")
	//}
	return nil
}

//go:embed templates/factory
var factory embed.FS

//go:embed templates/builder
var builder embed.FS

//go:embed templates/deployment
var deployment embed.FS
