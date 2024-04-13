package main

import (
	"context"
	"embed"
	"fmt"
	"github.com/codefly-dev/core/agents/communicate"
	dockerhelpers "github.com/codefly-dev/core/agents/helpers/docker"
	"github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/configurations/standards"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	agentv0 "github.com/codefly-dev/core/generated/go/services/agent/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/services/builder/v0"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"
	"github.com/codefly-dev/core/wool"
)

type ImportRoute struct {
	*configurations.RestRoute
	service     string
	application string
}

type ImportGRPC struct {
	*configurations.GRPCRoute
}

func (g *ImportGRPC) Unique() string {
	return fmt.Sprintf("%s%s %s", g.Application, g.Service, g.Name)

}

func (imp *ImportRoute) Unique() string {
	return fmt.Sprintf("%s%s %s", configurations.ServiceUnique(imp.application, imp.service), imp.RestRoute.Path, imp.RestRoute.Method)
}

type Builder struct {
	*Service

	syncForREST []*ImportRoute
}

func NewBuilder() *Builder {
	return &Builder{
		Service: NewService(),
	}
}

func (s *Builder) Load(ctx context.Context, req *builderv0.LoadRequest) (*builderv0.LoadResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	err := s.Base.Load(ctx, req.Identity, s.Settings)
	if err != nil {
		return nil, err
	}

	requirements.Localize(s.Location)

	s.Setup()

	_, err = shared.CheckDirectoryOrCreate(ctx, s.restRoutesLocation)
	if err != nil {
		return s.Builder.LoadError(err)
	}

	// communication on CreateResponse
	err = s.Communication.Register(ctx, communicate.New[builderv0.CreateRequest](createCommunicate()))
	if err != nil {
		return s.Builder.LoadError(err)
	}

	s.Builder.GettingStarted, err = templates.ApplyTemplateFrom(ctx, shared.Embed(factoryFS), "templates/factory/GETTING_STARTED.md", s.Information)
	if err != nil {
		return nil, err
	}

	if req.AtCreate {
		return s.Builder.LoadResponse()
	}

	s.Endpoints, err = s.Base.Service.LoadEndpoints(ctx)
	if err != nil {
		return s.Builder.LoadError(err)
	}

	err = s.LoadRestRoutes(ctx)
	if err != nil {
		return s.Builder.LoadError(err)
	}

	return s.Builder.LoadResponse()
}

func (s *Builder) Init(ctx context.Context, req *builderv0.InitRequest) (*builderv0.InitResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	s.DependencyEndpoints = req.DependenciesEndpoints

	err := s.UpdateAvailableRoutes(ctx)

	if err != nil {
		return s.Builder.InitError(err)
	}
	//
	//hash, err := requirements.Hash(ctx)
	//if err != nil {
	//	return s.Builder.InitError(err)
	//}

	return s.Builder.InitResponse()
}

func (s *Builder) Update(ctx context.Context, req *builderv0.UpdateRequest) (*builderv0.UpdateResponse, error) {
	defer s.Wool.Catch()

	return &builderv0.UpdateResponse{}, nil
}

func (s *Builder) UnknownRestRoutes(ctx context.Context) ([]*configurations.RestRouteGroup, error) {
	defer s.Wool.Catch()

	s.Wool.Debug("examining REST routes from dependency endpoints", wool.SliceCountField(s.DependencyEndpoints))
	// supported routes should correspond to dependency endpoints
	var updatedGroups []*RestRouteGroup
	for _, group := range s.RestRouteGroups {
		baseGroup := configurations.UnwrapRestRouteGroup(group)
		matchingEndpoint := configurations.FindEndpointForRestRoute(ctx, s.DependencyEndpoints, baseGroup)
		if matchingEndpoint != nil {
			updatedGroups = append(updatedGroups, group)
			continue
		}
		err := baseGroup.Delete(ctx, s.restRoutesLocation)
		if err != nil {
			return nil, s.Wool.Wrapf(err, "cannot delete group")
		}
	}

	var known []*configurations.RestRouteGroup
	for _, group := range updatedGroups {
		known = append(known, configurations.UnwrapRestRouteGroup(group))
	}
	s.Wool.Debug("known route groups", wool.SliceCountField(known))

	return configurations.DetectNewRoutesFromEndpoints(ctx, s.DependencyEndpoints, known), nil
}

func (s *Builder) UpdateAvailableRoutes(ctx context.Context) error {
	defer s.Wool.Catch()

	newRestRoutes, err := s.UnknownRestRoutes(ctx)
	s.Wool.Debug("unknown REST groups", wool.SliceCountField(newRestRoutes))

	s.syncForREST = []*ImportRoute{}
	for _, group := range newRestRoutes {
		for _, route := range group.Routes {
			// Create the extended route
			imp := &ImportRoute{RestRoute: route, service: group.Service, application: group.Application}
			s.Wool.Debug("application", wool.Field("application", exposeRestWithoutAuth(imp)))
			s.syncForREST = append(s.syncForREST, imp)
		}
	}

	if len(s.syncForREST) == 0 {
		return nil
	}
	s.Wool.Debug("found new routes", wool.SliceCountField(s.syncForREST))

	// register communication for Sync
	err = s.Communication.Register(ctx, communicate.New[builderv0.SyncRequest](s.syncQuestions()))
	if err != nil {
		return s.Wool.Wrapf(err, "cannot communicate for syncForREST")
	}

	return nil
}

func exposeRestWithAuth(imp *ImportRoute) string {
	return fmt.Sprintf("expose-rest-with-auth-%s", imp.Unique())
}
func exposeRestWithoutAuth(imp *ImportRoute) string {
	return fmt.Sprintf("expose-rest-without-auth-%s", imp.Unique())
}
func hiddenRest(imp *ImportRoute) string {
	return fmt.Sprintf("hidden-rest-%s", imp.Unique())
}

func (s *Builder) syncQuestions() *communicate.Sequence {
	var questions []*agentv0.Question
	if len(s.syncForREST) > 0 {
		s.Wool.Info("Detected new REST routes! Let's do some import")
	}
	for _, imp := range s.syncForREST {
		s.Wool.Debug("new route", wool.Field("route", imp.Unique()))
		questions = append(questions,
			communicate.NewChoice(&agentv0.Message{Name: imp.Unique(),
				Message:     fmt.Sprintf("Want to expose REST route: %s %s for service <%s> from application <%s>", imp.Path, imp.Method, imp.service, imp.application),
				Description: fmt.Sprintf("Corresponding route on the API service will be /%s/%s%s", imp.application, imp.service, imp.Path)},
				&agentv0.Message{Name: exposeRestWithAuth(imp), Message: "Yes (authenticated)"},
				&agentv0.Message{Name: exposeRestWithoutAuth(imp), Message: "Yes (non authenticated)"},
				&agentv0.Message{Name: hiddenRest(imp), Message: "No (internal only)"}),
		)
	}

	return communicate.NewSequence(questions...)
}

func (s *Builder) Sync(ctx context.Context, req *builderv0.SyncRequest) (*builderv0.SyncResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	session, err := s.Communication.Done(ctx, communicate.Channel[builderv0.SyncRequest]())
	if err != nil {
		return s.Builder.SyncError(err)
	}
	if session == nil {
		return s.Builder.SyncResponse()
	}
	s.Wool.Debug("states", wool.NullableField("answers", session.GetState()))

	restRouteLoader, err := configurations.NewExtendedRestRouteLoader[Extension](ctx, s.restRoutesLocation)
	if err != nil {
		return s.Builder.SyncError(err)
	}
	err = restRouteLoader.Load(ctx)
	if err != nil {
		return s.Builder.SyncError(err)
	}

	for _, imp := range s.syncForREST {
		expose, err := session.Choice(imp.Unique())
		if err != nil {
			return s.Builder.SyncError(err)
		}
		group := restRouteLoader.GroupFor(configurations.ServiceUnique(imp.application, imp.service), imp.Path)
		if group == nil {
			group = &RestRouteGroup{Application: imp.application, Service: imp.service, Path: imp.Path}
			restRouteLoader.AddGroup(group)
		}
		route := RestRoute{RestRoute: *imp.RestRoute}
		if expose.Option == hiddenRest(imp) {
			continue
		}
		s.Wool.Debug("exposing", wool.Field("key", expose.Option))
		route.Extension.Exposed = true
		if expose.Option == exposeRestWithAuth(imp) {
			route.Extension.Protected = true
		}
		group.Add(route)
	}
	err = restRouteLoader.Save(ctx)
	if err != nil {
		return s.Builder.SyncError(err)
	}

	// Get all the routes
	err = s.LoadRestRoutes(ctx)
	if err != nil {
		return s.Builder.SyncError(err)
	}

	return s.Builder.SyncResponse()
}

type Env struct {
	Key   string
	Value string
}

type DockerTemplating struct {
	Envs []Env
}

func (s *Builder) Build(ctx context.Context, req *builderv0.BuildRequest) (*builderv0.BuildResponse, error) {
	defer s.Wool.Catch()

	s.Wool.Debug("building docker image")

	docker := DockerTemplating{}
	err := s.Templates(ctx, docker, services.WithBuilder(builderFS))
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot copy and apply template")
	}

	build, err := dockerhelpers.NewBuilder(dockerhelpers.BuilderConfiguration{
		Root:        s.Location,
		Dockerfile:  "codefly/builder/Dockerfile",
		Destination: image,
		Output:      s.Wool,
	})
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot create builder")
	}
	_, err = build.Build(ctx)
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot build image")
	}
	return s.Builder.BuildResponse()
}

func (s *Builder) Deploy(ctx context.Context, req *builderv0.DeploymentRequest) (*builderv0.DeploymentResponse, error) {
	defer s.Wool.Catch()

	var k *builderv0.KubernetesDeployment
	var err error
	if k, err = s.Builder.KubernetesDeploymentRequest(ctx, req); err != nil {
		return s.Builder.DeployError(err)
	}

	conf, err := s.createConfig(ctx, req.DependenciesNetworkMappings, basev0.NetworkScope_Container)
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot write config")
	}

	params := services.DeploymentParameters{
		Parameters: string(conf)}

	err = s.Builder.KustomizeDeploy(ctx, req.Environment, k, deploymentFS, params)
	if err != nil {
		return s.Builder.DeployError(err)
	}

	return s.Builder.DeployResponse()
}

/* Creation */

const Watch = "with-hot-reload"

func createCommunicate() *communicate.Sequence {
	return communicate.NewSequence(
		communicate.NewConfirm(&agentv0.Message{Name: Watch, Message: "Hot-reload on route changes?", Description: "codefly can restart your service when route changes are detected after a sync 🔎"}, true),
	)
}

func (s *Builder) Create(ctx context.Context, req *builderv0.CreateRequest) (*builderv0.CreateResponse, error) {
	defer s.Wool.Catch()

	session, err := s.Communication.Done(ctx, communicate.Channel[builderv0.CreateRequest]())
	if err != nil {
		return s.Builder.CreateError(err)
	}

	s.Settings.Watch, err = session.Confirm(Watch)
	if err != nil {
		return s.Builder.CreateError(err)
	}

	err = s.Templates(ctx, s.Information, services.WithFactory(factoryFS))
	if err != nil {
		return s.Builder.CreateError(err)
	}

	err = s.CreateEndpoint(ctx)
	if err != nil {
		return s.Builder.CreateError(err)
	}

	err = s.Templates(ctx, s.Information, services.WithTemplate(routingFS, "routing", "routing"))
	if err != nil {
		return s.Builder.CreateError(err)
	}

	return s.Base.Builder.CreateResponse(ctx, s.Settings)
}

func (s *Service) CreateEndpoint(ctx context.Context) error {
	defer s.Wool.Catch()
	endpoint := s.Base.Service.BaseEndpoint(standards.REST)
	endpoint.Visibility = configurations.VisibilityPublic
	rest, err := configurations.LoadRestAPI(ctx, nil)
	if err != nil {
		return s.Wool.Wrapf(err, "cannot load HTTP api")
	}
	s.restEndpoint, err = configurations.NewAPI(ctx, endpoint, configurations.ToRestAPI(rest))
	if err != nil {
		return s.Wool.Wrapf(err, "cannot create openapi api")
	}
	s.Endpoints = []*basev0.Endpoint{s.restEndpoint}
	return nil
}

//go:embed templates/factory
var factoryFS embed.FS

//go:embed templates/routing
var routingFS embed.FS

//go:embed templates/builder
var builderFS embed.FS

//go:embed templates/deployment
var deploymentFS embed.FS
