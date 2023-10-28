package main

import (
	"context"
	"github.com/codefly-dev/cli/pkg/application"
	"github.com/codefly-dev/cli/pkg/plugins"
	"github.com/codefly-dev/cli/pkg/plugins/communicate"
	"github.com/codefly-dev/cli/pkg/plugins/helpers/code"
	"github.com/codefly-dev/cli/pkg/plugins/services"
	corev1 "github.com/codefly-dev/cli/proto/v1/core"
	runtimev1 "github.com/codefly-dev/cli/proto/v1/services/runtime"
	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/shared"
	openapiloads "github.com/go-openapi/loads"
	openapispec "github.com/go-openapi/spec"
	"github.com/pkg/errors"
	"path"
	"strings"
	"sync"
)

type Runtime struct {
	*Service
	Identity      *runtimev1.ServiceIdentity
	Configuration *configurations.Service

	ServiceLogger *plugins.ServiceLogger

	// method states
	sync *Sync

	// internal
	status services.InformationStatus

	mutex   *sync.Mutex
	events  chan code.Change
	watcher *code.Watcher
	Runner  *code.Runner
}

func NewRuntime() *Runtime {
	service := NewService()
	return &Runtime{
		Service: service,
		sync:    &Sync{logger: service.PluginLogger},
		mutex:   &sync.Mutex{},
	}
}

func (p *Runtime) Local(f string) string {
	return path.Join(p.Location, f)
}

func (p *Runtime) Configure(req *runtimev1.ConfigureRequest) (*runtimev1.ConfigureResponse, error) {
	defer p.PluginLogger.Catch()

	err := configurations.LoadSpec(req.Spec, &p.Spec, shared.BaseLogger(p.PluginLogger))
	if err != nil {
		return nil, errors.Wrapf(err, "factory[init]: cannot load spec")
	}

	if req.Debug {
		p.PluginLogger.SetDebug() // For developers
	}

	p.Location = req.Location
	p.Identity = req.Identity

	p.Configuration, err = configurations.LoadFromDir[configurations.Service](p.Location)
	if err != nil {
		return nil, shared.Wrapf(err, "cannot load service configuration")
	}
	p.ServiceLogger = plugins.NewServiceLogger(p.Identity.Name)

	p.InitEndpoints()

	p.PluginLogger.Info("%s -> configure", p.Identity.Name)

	return &runtimev1.ConfigureResponse{}, nil
}

func (p *Runtime) Init(req *runtimev1.InitRequest) (*runtimev1.InitResponse, error) {
	defer p.PluginLogger.Catch()

	p.status = services.Init

	p.PluginLogger.Debugf("creating event channel")
	p.events = make(chan code.Change)

	envs := []string{"FC_ENABLE=1",
		"FC_OUT=/etc/krakend/out.json",
		"FC_PARTIALS=config/partials",
		"FC_SETTINGS=config/settings/local",
		"FC_TEMPLATES=config/templates"}

	p.Runner = &code.Runner{
		Name:          p.Identity.Name,
		Bin:           "krakend",
		Args:          []string{"run", "--config", "krakend.config"},
		Envs:          envs,
		Dir:           p.Location,
		Debug:         true,
		ServiceLogger: p.ServiceLogger,
		PluginLogger:  p.PluginLogger,
		Cmd:           nil,
	}

	err := p.Runner.Init(context.Background())
	if err != nil {
		return &runtimev1.InitResponse{
			Status: &runtimev1.InitStatus{
				Status:  runtimev1.InitStatus_ERROR,
				Message: err.Error(),
			},
		}, nil
	}

	return &runtimev1.InitResponse{
		Status: &runtimev1.InitStatus{
			Status: runtimev1.InitStatus_READY,
		},
	}, nil
}

func (p *Runtime) Start(req *runtimev1.StartRequest) (*runtimev1.StartResponse, error) {
	defer p.PluginLogger.Catch()

	p.PluginLogger.Info("%s: network mapping: %v", p.Identity.Name, req.NetworkMappings)

	tracker, err := p.Runner.Run(context.Background())
	if err != nil {
		return &runtimev1.StartResponse{
			Status: &runtimev1.StartStatus{
				Status:  runtimev1.StartStatus_ERROR,
				Message: err.Error(),
			},
		}, nil
	}

	return &runtimev1.StartResponse{
		Status: &runtimev1.StartStatus{
			Status: runtimev1.StartStatus_STARTED,
		},
		Trackers: []*runtimev1.Tracker{tracker.Proto()},
	}, nil
}

func (p *Runtime) Information(req *runtimev1.InformationRequest) (*runtimev1.InformationResponse, error) {
	return &runtimev1.InformationResponse{Status: p.status}, nil
}

func (p *Runtime) Stop(req *runtimev1.StopRequest) (*runtimev1.StopResponse, error) {
	defer p.PluginLogger.Catch()

	p.PluginLogger.Debugf("%s: stopping service", p.Identity.Name)

	p.status = services.Stopped
	close(p.events)
	return &runtimev1.StopResponse{}, nil
}

type Sync struct {
	ready    bool
	logger   *plugins.PluginLogger
	err      error
	patience int
}

func (s *Sync) Ready() bool {
	return s.ready
}

func (s *Sync) communicate(req *corev1.Question) (*corev1.Answer, error) {
	s.patience++
	if s.patience > 2 {
		return &corev1.Answer{Done: true, Error: "patience exceeded"}, nil
	}
	if s.err != nil {
		return &corev1.Answer{Done: s.ready}, nil
	}
	return &corev1.Answer{}, nil
}

func (s *Sync) Stop() {
	s.ready = true
}

func (s *Sync) Init(endpoints *corev1.GroupEndpoints) {
	s.logger.DebugMe("SYNC STATE ENDPOINT")
	for _, app := range endpoints.ApplicationEndpoints {
		for _, svc := range app.ServiceEndpoints {
			for _, endpoint := range svc.Endpoints {
				if openapi := endpoint.Api.GetOpenapi(); openapi != nil {
					swagger, err := parseOpenApi(openapi.Spec)
					if err != nil {
						s.err = err
						s.Stop()
					}
					for p, _ := range swagger.Paths.Paths {
						s.logger.DebugMe("GOT A PATH %v", p)
					}
				}
			}
		}
	}
}

func parseOpenApi(spec []byte) (*openapispec.Swagger, error) {
	analyzed, err := openapiloads.Analyzed(spec, "2.0")
	if err != nil {
		return nil, err
	}
	return analyzed.Spec(), nil
}

func (p *Runtime) Sync(req *runtimev1.SyncRequest) (*runtimev1.SyncResponse, error) {
	defer p.PluginLogger.Catch()

	if !p.sync.Ready() {
		p.sync.Init(req.Endpoints)
		return &runtimev1.SyncResponse{NeedCommunication: true}, nil
	}

	p.PluginLogger.Debugf("running sync: %v", p.Location)

	p.PluginLogger.Info("endpoints:\n%v", application.PrettyGroupEndpoints(req.Endpoints))

	return &runtimev1.SyncResponse{}, nil
}

func (p *Runtime) Build(req *runtimev1.BuildRequest) (*runtimev1.BuildResponse, error) {
	defer p.PluginLogger.Catch()

	p.PluginLogger.Debugf("building docker image")

	return &runtimev1.BuildResponse{}, nil
}

func (p *Runtime) Deploy(req *runtimev1.DeploymentRequest) (*runtimev1.DeploymentResponse, error) {
	defer p.PluginLogger.Catch()
	return &runtimev1.DeploymentResponse{}, nil
}

func (p *Runtime) Communicate(req *corev1.Question) (*corev1.Answer, error) {
	defer p.PluginLogger.Catch()

	switch req.Method {
	case communicate.Sync:
		return p.sync.communicate(req)
	}
	return &corev1.Answer{}, nil
}

/* Details

 */

func (p *Runtime) setupWatcher() error {
	p.PluginLogger.DebugMe("%s: watching for changes", p.Identity.Name)
	var err error
	p.watcher, err = code.NewWatcher(p.PluginLogger, p.events, p.Location, []string{"."}, "service.codefly.yaml")
	if err != nil {
		return err
	}
	go p.watcher.Start()

	go func() {
		for event := range p.events {
			p.PluginLogger.DebugMe("got an event: %v", event)
			if strings.Contains(event.Path, "proto") {
				_, err := p.Sync(&runtimev1.SyncRequest{})
				if err != nil {
					p.PluginLogger.Warn("cannot sync proto: %v", err)
				}
			}
			p.ServiceLogger.Info("-> Detected working code changes: restarting")
			p.PluginLogger.DebugMe("detected working code changes: restarting")
			p.mutex.Lock()
			p.status = services.RestartWanted
			p.mutex.Unlock()
		}
	}()
	return nil
}
