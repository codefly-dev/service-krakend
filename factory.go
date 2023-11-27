package main

import (
	"embed"
	"fmt"
	"github.com/codefly-dev/core/agents/communicate"
	"github.com/codefly-dev/core/agents/endpoints"
	dockerhelpers "github.com/codefly-dev/core/agents/helpers/docker"
	golanghelpers "github.com/codefly-dev/core/agents/helpers/go"
	"github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/configurations"
	agentsv1 "github.com/codefly-dev/core/proto/v1/go/agents"
	servicev1 "github.com/codefly-dev/core/proto/v1/go/services"
	factoryv1 "github.com/codefly-dev/core/proto/v1/go/services/factory"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"
	"os"
)

type Factory struct {
	*Service

	create         *communicate.ClientContext
	createSequence *communicate.Sequence
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

	err = p.LoadEndpoints()
	if err != nil {
		return p.FactoryInitResponseError(err)
	}

	channels, err := p.WithCommunications(services.NewDynamicChannel(communicate.Create))
	if err != nil {
		return nil, err
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

func (p *Factory) NewCreateCommunicate() (*communicate.ClientContext, error) {
	client, err := communicate.NewClientContext(p.Context(), communicate.Create)
	p.createSequence, err = client.NewSequence(
		client.NewConfirm(&agentsv1.Message{Name: Watch, Message: "Code hot-reload (Recommended)?", Description: "codefly can restart your service when code changes are detected 🔎"}, true),
		client.NewConfirm(&agentsv1.Message{Name: WithRest, Message: "Automatic REST generation (Recommended)?", Description: "codefly can generate a REST server that stays magically 🪄 synced to your gRPC definition -- the easiest way to do REST"}, true),
		client.NewConfirm(&agentsv1.Message{Name: WithRest, Message: "Kreya configuration?", Description: "codefly can create a Kreya configuration to make it easy to call your endpoints, because why would you want to do that manually? 😵‍💫"}, true),
	)
	if err != nil {
		return nil, err
	}
	return client, nil
}

type Deployment struct {
	Replicas int
}

type CreateConfiguration struct {
	Image      *configurations.DockerImage
	Deployment Deployment
	Domain     string
	Envs       []string
}

func (p *Factory) Create(req *factoryv1.CreateRequest) (*factoryv1.CreateResponse, error) {
	defer p.AgentLogger.Catch()

	if p.create == nil {
		// Initial setup
		var err error
		p.AgentLogger.DebugMe("Setup communication")
		p.create, err = p.NewCreateCommunicate()
		if err != nil {
			return nil, p.AgentLogger.Wrapf(err, "cannot setup up communication")
		}
		err = p.Wire(communicate.Create, p.create)
		if err != nil {
			return nil, p.AgentLogger.Wrapf(err, "cannot wire communication")
		}
		return &factoryv1.CreateResponse{NeedCommunication: true}, nil
	}

	// Make sure the communication for create has been done successfully
	if !p.create.Ready() {
		p.DebugMe("create not ready!")
		return nil, p.AgentLogger.Errorf("create: communication not ready")
	}

	p.Settings.Watch = p.create.Confirm(p.createSequence.Find(Watch)).Confirmed
	p.Settings.CreateHttpEndpoint = p.create.Confirm(p.createSequence.Find(WithRest)).Confirmed

	ignores := []string{"go.work", "service.generation.codefly.yaml"}
	err := p.Templates(p.Information, services.WithFactory(factory, ignores...),
		services.WithBuilder(builder),
		services.WithDeploymentFor(deployment, "kustomize/base"))
	if err != nil {
		return nil, err
	}

	out, err := shared.GenerateTree(p.Location, " ")
	if err != nil {
		return nil, err
	}
	p.AgentLogger.Info("tree: %s", out)
	p.ServiceLogger.Info("We generated this code for you:\n%s", out)

	err = p.CreateEndpoints()
	if err != nil {
		return nil, p.Wrapf(err, "cannot create endpoints")
	}

	helper := golanghelpers.Go{Dir: p.Location}

	err = helper.BufGenerate(p.AgentLogger)
	if err != nil {
		return nil, fmt.Errorf("factory>create: go helper: cannot run buf generate: %v", err)
	}
	err = helper.ModTidy(p.AgentLogger)
	if err != nil {
		return nil, fmt.Errorf("factory>create: go helper: cannot run mod tidy: %v", err)
	}

	return p.Base.Create(p.Settings, p.Endpoints...)
}

func (p *Factory) Update(req *factoryv1.UpdateRequest) (*factoryv1.UpdateResponse, error) {
	defer p.AgentLogger.Catch()

	p.ServiceLogger.Info("Updating")

	err := p.Base.Templates(nil, services.WithBuilder(builder))
	if err != nil {
		return nil, p.Wrapf(err, "cannot copy and apply template")
	}

	helper := golanghelpers.Go{Dir: p.Location}
	err = helper.Update(p.AgentLogger)
	if err != nil {
		return nil, fmt.Errorf("factory>update: go helper: cannot run update: %v", err)
	}
	return &factoryv1.UpdateResponse{}, nil
}

func (p *Factory) Sync(req *factoryv1.SyncRequest) (*factoryv1.SyncResponse, error) {
	defer p.AgentLogger.Catch()

	p.AgentLogger.TODO("Some caching please!")

	p.AgentLogger.Debugf("running sync: %v", p.Location)
	helper := golanghelpers.Go{Dir: p.Location}

	// Clean-up the generated code
	p.AgentLogger.TODO("get location of generated code from buf")
	err := os.RemoveAll(p.Local("adapters/servicev1"))
	if err != nil {
		return nil, p.Wrapf(err, "cannot remove adapters")
	}
	// Re-generate
	err = helper.BufGenerate(p.AgentLogger)
	if err != nil {
		return nil, p.Wrapf(err, "cannot generate proto")
	}
	err = helper.ModTidy(p.AgentLogger)
	if err != nil {
		return nil, p.Wrapf(err, "cannot tidy go.mod")
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
	docker := DockerTemplating{}

	e, err := endpoints.FromProtoEndpoint(p.GrpcEndpoint)
	if err != nil {
		return nil, p.Wrapf(err, "cannot convert grpc endpoint")
	}
	gRPC := configurations.AsEndpointEnvironmentVariableKey(p.Configuration.Application, p.Configuration.Name, e)
	docker.Envs = append(docker.Envs, Env{Key: gRPC, Value: "localhost:9090"})
	if p.RestEndpoint != nil {
		e, err = endpoints.FromProtoEndpoint(p.RestEndpoint)
		if err != nil {
			return nil, p.Wrapf(err, "cannot convert grpc endpoint")
		}
		rest := configurations.AsEndpointEnvironmentVariableKey(p.Configuration.Application, p.Configuration.Name, e)
		docker.Envs = append(docker.Envs, Env{Key: rest, Value: "localhost:8080"})
	}

	err = os.Remove(p.Local("codefly/builder/Dockerfile"))
	if err != nil {
		return nil, p.Wrapf(err, "cannot remove dockerfile")
	}
	err = p.Templates(docker, services.WithBuilder(builder))
	if err != nil {
		return nil, p.Wrapf(err, "cannot copy and apply template")
	}
	builder, err := dockerhelpers.NewBuilder(dockerhelpers.BuilderConfiguration{
		Root:       p.Location,
		Dockerfile: "codefly/builder/Dockerfile",
		Image:      p.Identity.Name,
		Tag:        p.Configuration.Version,
	})
	if err != nil {
		return nil, p.Wrapf(err, "cannot create builder")
	}
	builder.WithLogger(p.AgentLogger)
	_, err = builder.Build()
	if err != nil {
		return nil, p.Wrapf(err, "cannot build image")
	}
	return &factoryv1.BuildResponse{}, nil
}

func (p *Factory) Deploy(req *factoryv1.DeploymentRequest) (*factoryv1.DeploymentResponse, error) {
	return &factoryv1.DeploymentResponse{}, nil
}

func (p *Factory) CreateEndpoints() error {
	grpc, err := endpoints.NewGrpcApi(&configurations.Endpoint{Name: "grpc"}, p.Local("api.proto"))
	if err != nil {
		return p.Wrapf(err, "cannot create grpc api")
	}
	p.Endpoints = append(p.Endpoints, grpc)

	if p.Settings.CreateHttpEndpoint {
		rest, err := endpoints.NewRestApiFromOpenAPI(p.Context(), &configurations.Endpoint{Name: "rest", Scope: "private"}, p.Local("api.swagger.json"))
		if err != nil {
			return p.Wrapf(err, "cannot create openapi api")
		}
		p.Endpoints = append(p.Endpoints, rest)
	}
	return nil
}

//go:embed templates/factory
var factory embed.FS

//go:embed templates/builder
var builder embed.FS

//go:embed templates/deployment
var deployment embed.FS
