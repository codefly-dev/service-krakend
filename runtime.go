package main

import (
	"context"
	"github.com/codefly-dev/core/agents/helpers/code"
	"github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/configurations"
	agentv0 "github.com/codefly-dev/core/generated/go/services/agent/v0"
	runtimev0 "github.com/codefly-dev/core/generated/go/services/runtime/v0"
	"github.com/codefly-dev/core/runners"
	"github.com/codefly-dev/core/wool"
)

type Runtime struct {
	*Service

	// internal
	runner *runners.Docker
}

func NewRuntime() *Runtime {
	return &Runtime{
		Service: NewService(),
	}
}

func (s *Runtime) Load(ctx context.Context, req *runtimev0.LoadRequest) (*runtimev0.LoadResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	err := s.Base.Load(ctx, req.Identity, s.Settings)
	if err != nil {
		return s.Base.Runtime.LoadError(err)
	}

	s.Setup()

	s.Wool.Debug("loading service")

	err = s.LoadRestRoutes(ctx)
	if err != nil {
		return s.Base.Runtime.LoadError(err)
	}

	//err = s.LoadGRPCRoutes(ctx)
	//if err != nil {
	//	return s.Base.Runtime.LoadError(err)
	//}

	err = s.LoadEndpoints(ctx)
	if err != nil {
		return s.Base.Runtime.LoadError(err)
	}

	s.openapiDestination = s.Local("swagger.json")

	if s.Settings.Watch && s.Watcher == nil {
		s.Wool.Debug("setting up code watcher")
		// Add proto and swagger
		dependencies := requirements.Clone()
		dependencies.Localize(s.Location)
		conf := services.NewWatchConfiguration(dependencies)
		err = s.SetupWatcher(ctx, conf, s.EventHandler)
		if err != nil {
			s.Wool.Warn("error in watcher", wool.ErrField(err))
		}
	}

	return s.Base.Runtime.LoadResponse()
}

func (s *Runtime) Init(ctx context.Context, req *runtimev0.InitRequest) (*runtimev0.InitResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	s.Wool.Debug("generating openapiDestination")

	err := s.writeOpenAPI(ctx, req.DependenciesEndpoints)
	if err != nil {
		return s.Runtime.InitError(err)
	}
	//
	//err = requirements.UpdateCache(ctx)
	//if err != nil {
	//	return s.Runtime.InitError(err)
	//}

	s.NetworkMappings = req.ProposedNetworkMappings

	//var updated bool
	//if updated, err = requirements.Updated(ctx); err != nil {
	//	return s.Runtime.InitError(err)
	//}
	//if !updated {
	//	s.Wool.Debug("no change in routing detected")
	//	return s.Runtime.InitResponse(s.networkMappings)
	//}

	net, err := configurations.FindNetworkMapping(s.restEndpoint, s.NetworkMappings)
	if err != nil {
		return s.Runtime.InitError(err)
	}

	s.LogForward("will run on: %s", net.Address)

	// for docker
	s.port = 80

	s.validator, err = s.CreateValidator(ctx, req.ProviderInfos)
	if err != nil {
		return s.Runtime.InitError(err)
	}

	if s.runner != nil {
		err = s.runner.Stop()
		if err != nil {
			return s.Runtime.InitError(err)
		}
	}

	runner, err := runners.NewDocker(ctx)
	if err != nil {
		return s.Runtime.InitError(err)
	}

	s.runner = runner

	s.runner.WithMount(s.Local("config"), "/app/config")

	s.runner.WithPort(runners.DockerPortMapping{Container: s.port, Host: net.Port})

	envs := []string{
		"FC_ENABLE=1",
		"FC_SETTINGS=/app/config/settings",
		"FC_CONFIG=/app/config/out.json",
	}

	s.runner.WithEnvironmentVariables(envs...)

	s.runner.WithCommand("krakend", "run", "-d", "-c", "/app/config/krakend.tmpl")

	err = s.runner.Init(ctx, image)
	if err != nil {
		return s.Runtime.InitError(err)
	}

	return s.Base.Runtime.InitResponse()
}

func (s *Runtime) Start(ctx context.Context, req *runtimev0.StartRequest) (*runtimev0.StartResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	if s.runner.Running() {
		return s.Runtime.StartResponse()
	}

	s.Wool.Debug("starting runtime", wool.NullableField("network mappings", configurations.MakeNetworkMappingSummary(req.OtherNetworkMappings)))

	// For docker, replace localhost by host.docker.internal
	configurations.LocalizeMappings(req.OtherNetworkMappings, "host.docker.internal")

	err := s.writeConfig(ctx, req.OtherNetworkMappings)
	if err != nil {
		return s.Runtime.StartError(err)
	}

	runningContext := s.Wool.Inject(context.Background())

	err = s.runner.Start(runningContext)
	if err != nil {
		return s.Runtime.StartError(err)
	}

	return s.Runtime.StartResponse()
}

func (s *Runtime) Information(ctx context.Context, req *runtimev0.InformationRequest) (*runtimev0.InformationResponse, error) {
	return s.Runtime.InformationResponse(ctx, req)
}

func (s *Runtime) Stop(ctx context.Context, req *runtimev0.StopRequest) (*runtimev0.StopResponse, error) {
	defer s.Wool.Catch()

	s.Wool.Debug("stopping service")
	err := s.runner.Stop()
	if err != nil {
		return s.Runtime.StopError(err)
	}

	err = s.Base.Stop()
	if err != nil {
		return s.Runtime.StopError(err)
	}
	return s.Runtime.StopResponse()
}

func (s *Runtime) Communicate(ctx context.Context, req *agentv0.Engage) (*agentv0.InformationRequest, error) {
	return s.Base.Communicate(ctx, req)
}

/* Details

 */

func (s *Runtime) EventHandler(event code.Change) error {
	s.Wool.Debug("event detected", wool.Field("event", event))
	s.Runtime.DesiredLoad()
	return nil
}
