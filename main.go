package main

import (
	"context"
	"embed"
	"fmt"
	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/templates"
	"github.com/codefly-dev/core/wool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/agents/services"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"github.com/codefly-dev/core/shared"
)

// Agent version
var agent = shared.Must(resources.LoadFromFs[resources.Agent](shared.Embed(info)))

var requirements = builders.NewDependencies(agent.Name,
	builders.NewDependency("service.codefly.yaml"),
	builders.NewDependency("routing"),
)

type Settings struct {
}

var runtimeImage = &resources.DockerImage{Name: "devopsfaith/krakend", Tag: "2.6"}

type Extension struct {
	Exposed   bool `yaml:"exposed"`
	Protected bool `yaml:"protected"`
}

// RestRoute extends the concept of RestRoute to add API Gateway concepts
type RestRoute = resources.ExtendedRestRoute[Extension]

// RestRouteGroup extends the concept of RestRouteGroup to add API Gateway concepts
type RestRouteGroup = resources.ExtendedRestRouteGroup[Extension]

type Service struct {
	*services.Base

	// Access
	port uint16

	restRoutesLocation string

	RestRouteGroups []*RestRouteGroup

	validators []*AuthValidator

	// Settings
	*Settings

	restEndpoint       *basev0.Endpoint
	openapiDestination string
}

func (s *Service) Setup() {
	s.restRoutesLocation = s.Local("routing/rest")
}

func (s *Service) GetAgentInformation(ctx context.Context, _ *agentv0.AgentInformationRequest) (*agentv0.AgentInformation, error) {

	rm, err := templates.ApplyTemplateFrom(ctx, shared.Embed(readme), "templates/agent/README.md", s.Information)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &agentv0.AgentInformation{
		Capabilities: []*agentv0.Capability{
			{Type: agentv0.Capability_BUILDER},
			{Type: agentv0.Capability_RUNTIME},
		},
		Protocols: []*agentv0.Protocol{
			{Type: agentv0.Protocol_HTTP},
		},
		ReadMe: rm,
	}, nil
}

func NewService() *Service {
	return &Service{
		Base:     services.NewServiceBase(context.Background(), agent.Of(resources.ServiceAgent)),
		Settings: &Settings{},
	}
}

// LoadRestRoutes from routing configuration folder
func (s *Service) LoadRestRoutes(ctx context.Context) error {
	loader, err := resources.NewExtendedRestRouteLoader[Extension](ctx, s.restRoutesLocation)
	if err != nil {
		return s.Wool.Wrapf(err, "cannot create route loader")
	}
	err = loader.Load(ctx)
	if err != nil {
		return s.Wool.Wrapf(err, "cannot load routes")
	}
	s.RestRouteGroups = loader.Groups()
	s.Wool.Debug("known REST route groups", wool.SliceCountField(s.RestRouteGroups))
	return nil
}

type ValidatorConfiguration struct {
	Jwt *struct {
		Audience string `yaml:"audience"`
		URL      string `yaml:"url"`
	} `yaml:"jwt"`
}

func (s *Service) CreateValidators(ctx context.Context, conf *basev0.Configuration) ([]*AuthValidator, error) {
	s.Wool.Debug("conf", wool.Field("conf", resources.MakeConfigurationSummary(conf)))
	auth, err := resources.GetConfigurationInformation(ctx, conf, "auth")
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot get auth configuration")
	}
	if auth == nil {
		return nil, nil
	}
	s.Wool.Focus("AUTH", wool.Field("auth", string(auth.Data.Content)))
	var auths []*AuthValidator
	var vc ValidatorConfiguration
	err = configurations.InformationUnmarshal(auth, &vc)
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot unmarshal auth configuration")
	}
	if vc.Jwt != nil {
		jwtConf := JWTAuthValidator{
			Alg:             "RS256",
			Audience:        []string{vc.Jwt.Audience},
			JwkURL:          fmt.Sprintf("%s/.well-known/jwks.json", vc.Jwt.URL),
			PropagateClaims: [][]string{{"sub", wool.Header(wool.UserAuthIDKey)}},
			Cache:           true,
		}
		auths = append(auths,
			&AuthValidator{
				Key:           JWTAuthValidatorKey,
				Configuration: jwtConf},
		)
	}

	return auths, nil

}

func main() {
	agents.Register(
		services.NewServiceAgent(agent.Of(resources.ServiceAgent), NewService()),
		services.NewBuilderAgent(agent.Of(resources.RuntimeServiceAgent), NewBuilder()),
		services.NewRuntimeAgent(agent.Of(resources.BuilderServiceAgent), NewRuntime()))
}

//go:embed agent.codefly.yaml
var info embed.FS

//go:embed templates/agent
var readme embed.FS
