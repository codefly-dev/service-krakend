package main

import (
	"embed"

	"github.com/codefly-dev/cli/pkg/plugins/services"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	corev1 "github.com/codefly-dev/cli/proto/v1/core"
	servicev1 "github.com/codefly-dev/cli/proto/v1/services"
	factoryv1 "github.com/codefly-dev/cli/proto/v1/services/factory"
	"github.com/codefly-dev/core/configurations"
)

type Factory struct {
	*Service
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

	err := p.Base.Init(req, p.Spec)
	if err != nil {
		return nil, err
	}
	p.PluginLogger.Debugf("[factory::init] %v", req)

	return &factoryv1.InitResponse{
		Version: p.Version(),
	}, nil
}

func (p *Factory) Create(req *factoryv1.CreateRequest) (*factoryv1.CreateResponse, error) {
	defer p.PluginLogger.Catch()
	err := p.Base.PreCreate(req, p.Spec)
	if err != nil {
		return nil, err
	}

	create := CreateConfiguration{
		Name:      cases.Title(language.English, cases.NoLower).String(p.Identity.Name),
		Domain:    p.Base.Identity.Domain,
		Namespace: p.Base.Identity.Namespace,
		Readme:    Readme{Summary: p.Base.Identity.Name},
	}

	err = p.Templates(create, services.WithFactory(factory), services.WithBuilder(builder), services.WithRoutes(routes))
	if err != nil {
		return nil, err
	}

	endpoints, err := p.CreateEndpoints()
	if err != nil {
		return nil, p.Wrapf(err, "cannot create endpoints")
	}

	return p.Base.PostCreate(p.Spec, endpoints...)
}

func (p *Factory) Update(req *factoryv1.UpdateRequest) (*factoryv1.UpdateResponse, error) {
	return &factoryv1.UpdateResponse{}, nil
}

func (p *Factory) Communicate(req *corev1.Engage) (*corev1.InformationRequest, error) {
	// TODO implement me
	panic("implement me")
}

func (p *Factory) CreateEndpoints() ([]*corev1.Endpoint, error) {
	rest, err := services.NewHttpApi(&configurations.Endpoint{Name: p.Identity.Name, Public: true})
	if err != nil {
		return nil, p.Wrapf(err, "cannot  create tcp endpoint")
	}
	return []*corev1.Endpoint{rest}, nil
}

//go:embed templates/factory
var factory embed.FS

//go:embed templates/builder
var builder embed.FS

//go:embed templates/routes
var routes embed.FS
