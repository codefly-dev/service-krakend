package main

import (
	"context"
	"embed"
	"fmt"
	"github.com/codefly-dev/core/agents/communicate"
	dockerhelpers "github.com/codefly-dev/core/agents/helpers/docker"
	"github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	agentv0 "github.com/codefly-dev/core/generated/go/services/agent/v0"
	factoryv0 "github.com/codefly-dev/core/generated/go/services/factory/v0"
	runtimev0 "github.com/codefly-dev/core/generated/go/services/runtime/v0"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"
	"github.com/codefly-dev/core/wool"
	"os"
)

type Factory struct {
	*Service

	syncRoutes []*configurations.RestRoute
}

func NewFactory() *Factory {
	return &Factory{
		Service: NewService(),
	}
}

func (s *Factory) Load(ctx context.Context, req *factoryv0.LoadRequest) (*factoryv0.LoadResponse, error) {
	defer s.Wool.Catch()

	err := s.Base.Load(ctx, req.Identity, s.Settings)
	if err != nil {
		return nil, err
	}

	s.RoutesLocation = s.Local("routing")
	_, err = shared.CheckDirectoryOrCreate(ctx, s.RoutesLocation)
	if err != nil {
		return s.Factory.LoadError(err)
	}

	err = s.LoadEndpoint(ctx)
	if err != nil {
		return s.Factory.LoadError(err)
	}

	err = s.LoadRoutes(ctx)
	if err != nil {
		return s.Factory.LoadError(err)
	}

	gettingStarted, err := templates.ApplyTemplateFrom(shared.Embed(factory), "templates/factory/GETTING_STARTED.md", s.Information)
	if err != nil {
		return nil, err
	}

	// communication on Sync
	err = s.Communication.Register(ctx, communicate.New[factoryv0.CreateRequest](createCommunicate()))
	if err != nil {
		return s.Factory.LoadError(err)
	}

	return s.Factory.LoadResponse(gettingStarted)
}

const Public = "public"

func createCommunicate() *communicate.Sequence {
	return communicate.NewSequence(
		communicate.NewConfirm(&agentv0.Message{Name: Public, Message: "Public API?", Description: "Exposed to external users"}, false),
	)
}

func (s *Factory) Create(ctx context.Context, req *factoryv0.CreateRequest) (*factoryv0.CreateResponse, error) {
	defer s.Wool.Catch()

	session, err := s.Communication.Done(ctx, communicate.Channel[factoryv0.CreateRequest]())
	if err != nil {
		return s.Factory.CreateError(err)
	}

	s.Settings.Public, err = session.Confirm(Public)
	if err != nil {
		return s.Factory.CreateError(err)
	}
	err = s.Templates(ctx, s.Information, services.WithFactory(factory), services.WithBuilder(builder))
	if err != nil {
		return nil, err
	}

	return s.Base.Factory.CreateResponse(ctx, s.Settings)
}

func (s *Factory) Init(ctx context.Context, req *factoryv0.InitRequest) (*factoryv0.InitResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	s.DependencyEndpoints = req.DependenciesEndpoints

	err := s.UpdateAvailableRoutes(ctx)

	if err != nil {
		return s.Factory.InitError(err)
	}

	hash, err := requirements.Hash(ctx)
	if err != nil {
		return s.Factory.InitError(err)
	}

	return s.Factory.InitResponse(hash)
}

func (s *Factory) Update(ctx context.Context, req *factoryv0.UpdateRequest) (*factoryv0.UpdateResponse, error) {
	defer s.Wool.Catch()

	return &factoryv0.UpdateResponse{}, nil
}

func (s *Factory) UpdateAvailableRoutes(ctx context.Context) error {
	defer s.Wool.Catch()

	s.Wool.Debug("examining routes from dependency endpoints", wool.SliceCountField(s.DependencyEndpoints))
	// supported routes should correspond to dependency endpoints
	for _, route := range s.Routes {
		matchingEndpoint := configurations.FindEndpointForRoute(ctx, s.DependencyEndpoints, configurations.UnwrapRoute(route))
		if matchingEndpoint == nil {
			s.Base.Info("found a route not matching to a dependency endpoint - deleting it")
		}
		err := route.Delete(ctx, s.RoutesLocation)
		if err != nil {
			return s.Wool.Wrapf(err, "cannot delete route")
		}
	}
	// unwrap routes
	knownRoutes := configurations.UnwrapRoutes(s.Routes)
	s.syncRoutes = configurations.DetectNewRoutesFromEndpoints(ctx, knownRoutes, s.DependencyEndpoints)

	if len(s.syncRoutes) == 0 {
		return nil
	}
	s.Wool.Debug("found new routes", wool.SliceCountField(s.syncRoutes))

	// register communication for Sync
	err := s.Communication.Register(ctx, communicate.New[factoryv0.SyncRequest](s.syncRoutesQuestions()))
	if err != nil {
		return s.Wool.Wrapf(err, "cannot communicate for sync")
	}

	return nil
}

func (s *Factory) syncRoutesQuestions() *communicate.Sequence {
	var questions []*agentv0.Question
	for _, route := range s.syncRoutes {
		questions = append(questions, communicate.NewConfirm(&agentv0.Message{Name: route.Unique(),
			Message:     fmt.Sprintf("Want to expose REST route: %s for service <%s> from application <%s>", route.Path, route.Service, route.Application),
			Description: fmt.Sprintf("Corresponding route on the API will be %s", route.Unique())}, true))
	}
	return communicate.NewSequence(questions...)
}

func (s *Factory) Sync(ctx context.Context, req *factoryv0.SyncRequest) (*factoryv0.SyncResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	session, err := s.Communication.Done(ctx, communicate.Channel[factoryv0.SyncRequest]())
	if err != nil {
		return s.Factory.SyncError(err)
	}
	if session == nil {
		return &factoryv0.SyncResponse{}, nil
	}
	s.Wool.Debug("states", wool.NullableField("answers", session.GetState()))
	// Save the routes
	for _, route := range s.syncRoutes {
		expose, err := session.Confirm(route.Unique())
		if err != nil {
			return s.Factory.SyncError(err)
		}
		if expose {
			err = route.Save(ctx, s.RoutesLocation)
			if err != nil {
				return s.Factory.SyncError(err)
			}
		}
	}
	// Get all the routes
	err = s.LoadRoutes(ctx)
	if err != nil {
		return s.Factory.SyncError(err)
	}
	combinator, err := configurations.NewOpenAPICombinator(ctx, configurations.FromProtoEndpoint(s.endpoint), s.DependencyEndpoints...)
	if err != nil {
		return s.Factory.SyncError(err)
	}
	combinator.WithDestination(s.Local("swagger.json"))
	combinator.WithVersion(s.Configuration.Version)
	for _, route := range s.Routes {
		s.Wool.Focus("EXPOSING", wool.Field("route", route))
		combinator.Only(route.ServiceUnique(), route.Path)
	}
	_, err = combinator.Combine(ctx)
	if err != nil {
		return s.Factory.SyncError(err)
	}
	return &factoryv0.SyncResponse{}, nil
}

type Env struct {
	Key   string
	Value string
}

type DockerTemplating struct {
	Envs []Env
}

func (s *Factory) Build(ctx context.Context, req *factoryv0.BuildRequest) (*factoryv0.BuildResponse, error) {
	s.Wool.Debug("building docker image")
	docker := DockerTemplating{}
	// We want to use DNS to create NetworkMapping
	networkMapping, err := s.Network(req.DependenciesEndpoints)
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot create network mapping")
	}
	conf, err := s.createConfig(ctx, networkMapping)
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot write config")
	}

	target := s.Local("codefly/builder/routing.json")
	err = os.WriteFile(target, conf, 0o644)
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot write settings to %s", target)
	}
	err = s.Templates(ctx, docker, services.WithBuilder(builder))
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot copy and apply template")
	}

	build, err := dockerhelpers.NewBuilder(dockerhelpers.BuilderConfiguration{
		Root:        s.Location,
		Dockerfile:  "codefly/builder/Dockerfile",
		Destination: s.DockerImage(),
	})
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot create builder")
	}
	_, err = build.Build(ctx)
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot build image")
	}
	return &factoryv0.BuildResponse{}, nil
}

type Deployment struct {
	Replicas int
}

type DeploymentParameter struct {
	Image *configurations.DockerImage
	*services.Information
	Deployment
}

func (s *Factory) Deploy(ctx context.Context, req *factoryv0.DeploymentRequest) (*factoryv0.DeploymentResponse, error) {
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
	return &factoryv0.DeploymentResponse{}, nil
}

func (s *Factory) Network(es []*basev0.Endpoint) ([]*runtimev0.NetworkMapping, error) {
	return nil, nil
	//s.DebugMe("in network: %v", configurations.Condensed(es))
	//pm, err := network.NewServiceDnsManager(ctx, s.Identity)
	//if err != nil {
	//	return nil, s.Wool.Wrapf(err, "cannot create network manager")
	//}
	//for _, endpoint := range es {
	//	err = pm.Expose(endpoint)
	//	if err != nil {
	//		return nil, s.Wool.Wrapf(err, "cannot add grpc endpoint to network manager")
	//	}
	//}
	//err = pm.Reserve()
	//if err != nil {
	//	return nil, s.Wool.Wrapf(err, "cannot reserve ports")
	//}
	//return pm.NetworkMapping()
}

//go:embed templates/factory
var factory embed.FS

//go:embed templates/builder
var builder embed.FS

//go:embed templates/deployment
var deployment embed.FS
