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

func (imp *ImportRoute) Unique() string {
	return fmt.Sprintf("%s/%s %s", configurations.ServiceUnique(imp.application, imp.application), imp.RestRoute.Path, imp.RestRoute.Method)
}

type Builder struct {
	*Service

	sync []*ImportRoute
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

	_, err = shared.CheckDirectoryOrCreate(ctx, s.RoutesLocation)
	if err != nil {
		return s.Builder.LoadError(err)
	}

	err = s.LoadEndpoints(ctx)
	if err != nil {
		return s.Builder.LoadError(err)
	}

	err = s.LoadRoutes(ctx)
	if err != nil {
		return s.Builder.LoadError(err)
	}

	gettingStarted, err := templates.ApplyTemplateFrom(ctx, shared.Embed(factoryFS), "templates/factory/GETTING_STARTED.md", s.Information)
	if err != nil {
		return nil, err
	}

	return s.Builder.LoadResponse(gettingStarted)
}

func (s *Builder) Init(ctx context.Context, req *builderv0.InitRequest) (*builderv0.InitResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	s.DependencyEndpoints = req.DependenciesEndpoints

	s.Wool.Debug("dependencies", wool.SliceCountField(s.DependencyEndpoints))

	validator, err := s.CreateValidator(ctx, req.ProviderInfos)
	if err != nil {
		return s.Builder.InitError(err)
	}
	s.Validator = validator

	err = s.UpdateAvailableRoutes(ctx)

	if err != nil {
		return s.Builder.InitError(err)
	}

	//hash, err := requirements.Hash(ctx)
	//if err != nil {
	//	return s.Builder.InitError(err)
	//}
	hash := "TODO"

	return s.Builder.InitResponse(hash)
}

func (s *Builder) Update(ctx context.Context, req *builderv0.UpdateRequest) (*builderv0.UpdateResponse, error) {
	defer s.Wool.Catch()

	return &builderv0.UpdateResponse{}, nil
}

func (s *Builder) UpdateAvailableRoutes(ctx context.Context) error {
	defer s.Wool.Catch()

	s.Wool.Debug("examining routes from dependency endpoints", wool.SliceCountField(s.DependencyEndpoints))
	// supported routes should correspond to dependency endpoints
	var updatedGroups []*RestRouteGroup
	for _, group := range s.RouteGroups {
		baseGroup := configurations.UnwrapRouteGroup(group)
		matchingEndpoint := configurations.FindEndpointForRoute(ctx, s.DependencyEndpoints, baseGroup)
		if matchingEndpoint != nil {
			updatedGroups = append(updatedGroups, group)
			continue
		}
		err := baseGroup.Delete(ctx, s.RoutesLocation)
		if err != nil {
			return s.Wool.Wrapf(err, "cannot delete group")
		}
	}

	var known []*configurations.RestRouteGroup
	for _, group := range updatedGroups {
		known = append(known, configurations.UnwrapRouteGroup(group))
	}
	s.Wool.Debug("known route groups", wool.SliceCountField(known))

	unknowns := configurations.DetectNewRoutesFromEndpoints(ctx, s.DependencyEndpoints, known)
	s.Wool.Debug("unknown route groups", wool.SliceCountField(unknowns))
	s.sync = []*ImportRoute{}
	for _, unknown := range unknowns {
		for _, route := range unknown.Routes {
			// Create the extended route
			imp := &ImportRoute{RestRoute: route, service: unknown.Service, application: unknown.Application}
			s.sync = append(s.sync, imp)
		}
	}

	if len(s.sync) == 0 {
		return nil
	}
	s.Wool.Debug("found new routes", wool.SliceCountField(s.sync))

	// register communication for Sync
	err := s.Communication.Register(ctx, communicate.New[builderv0.SyncRequest](s.syncQuestions()))
	if err != nil {
		return s.Wool.Wrapf(err, "cannot communicate for sync")
	}

	return nil
}

func requireAuthName(imp *ImportRoute) string {
	return fmt.Sprintf("require-auth-%s", imp.Unique())
}

func (s *Builder) syncQuestions() *communicate.Sequence {
	var questions []*agentv0.Question
	for _, imp := range s.sync {
		questions = append(questions, communicate.NewConfirm(&agentv0.Message{Name: imp.Unique(),
			Message:     fmt.Sprintf("Want to expose REST route: %s %s for service <%s> from application <%s>", imp.Path, imp.Method, imp.service, imp.application),
			Description: fmt.Sprintf("Corresponding route on the API service will be /%s/%s%s", imp.application, imp.service, imp.Path)}, true))
		questions = append(questions, communicate.NewConfirm(&agentv0.Message{Name: requireAuthName(imp),
			Message: "Requires Authentication?"}, true))
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
	loader, err := configurations.NewExtendedRouteLoader[Extension](ctx, s.RoutesLocation)
	if err != nil {
		return s.Builder.SyncError(err)
	}
	err = loader.Load(ctx)
	if err != nil {
		return s.Builder.SyncError(err)
	}

	for _, imp := range s.sync {
		expose, err := session.Confirm(imp.Unique())
		if err != nil {
			return s.Builder.SyncError(err)
		}
		s.Wool.Debug("exposing", wool.Field("imp", imp))
		group := loader.GroupFor(configurations.ServiceUnique(imp.application, imp.service), imp.Path)
		if group == nil {
			group = &RestRouteGroup{Application: imp.application, Service: imp.service, Path: imp.Path}
			loader.AddGroup(group)
		}
		route := RestRoute{RestRoute: *imp.RestRoute}
		route.Extension.Exposed = expose
		authRequired, err := session.Confirm(requireAuthName(imp))
		route.Extension.Protected = authRequired
		if err != nil {
			return s.Builder.SyncError(err)
		}

		group.Add(route)
	}
	err = loader.Save(ctx)
	if err != nil {
		return s.Builder.SyncError(err)
	}
	// Get all the routes
	err = s.LoadRoutes(ctx)
	if err != nil {
		return s.Builder.SyncError(err)
	}

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
		Destination: s.DockerImage(),
		Output:      s.Wool,
	})
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot create builder")
	}
	_, err = build.Build(ctx)
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot build image")
	}
	return &builderv0.BuildResponse{}, nil
}

type Deployment struct {
	Replicas int
}

type DeploymentParameter struct {
	Image *configurations.DockerImage
	*services.Information
	Deployment
	Settings string
}

func (s *Builder) Deploy(ctx context.Context, req *builderv0.DeploymentRequest) (*builderv0.DeploymentResponse, error) {
	defer s.Wool.Catch()

	conf, err := s.createConfig(ctx, req.NetworkMappings, false)
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot write config")
	}

	params := DeploymentParameter{
		Image:       s.DockerImage(),
		Information: s.Information,
		Deployment:  Deployment{Replicas: 1},
		Settings:    string(conf)}

	err = s.Builder.Deploy(ctx, req, deploymentFS, params)
	if err != nil {
		return s.Builder.DeployError(err)
	}

	return s.Builder.DeployResponse()
}

/* Creation */

func (s *Builder) Create(ctx context.Context, req *builderv0.CreateRequest) (*builderv0.CreateResponse, error) {
	defer s.Wool.Catch()

	err := s.Templates(ctx, s.Information, services.WithFactory(factoryFS))
	if err != nil {
		return s.Builder.CreateError(err)
	}
	s.Configuration.ProviderDependencies = []string{"auth0"}

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
	var err error
	var endpoint *basev0.Endpoint
	if shared.FileExists(s.Local("swagger.json")) {
		endpoint, err = configurations.NewRestAPIFromOpenAPI(ctx, &configurations.Endpoint{Name: standards.REST, API: standards.REST, Visibility: configurations.VisibilityPublic}, s.Local("swagger.json"))
		if err != nil {
			return s.Wool.Wrapf(err, "cannot  create rest endpoint")
		}
	} else {
		endpoint, err = configurations.NewRestAPI(ctx, &configurations.Endpoint{Name: standards.REST, API: standards.REST, Visibility: configurations.VisibilityPublic})
		if err != nil {
			return s.Wool.Wrapf(err, "cannot  create rest endpoint")
		}
	}
	endpoint.Application = s.Configuration.Application
	endpoint.Service = s.Configuration.Name
	s.endpoint = endpoint
	s.Endpoints = []*basev0.Endpoint{s.endpoint}
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
