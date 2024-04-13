package main

import (
	"context"
	builderv0 "github.com/codefly-dev/core/generated/go/services/builder/v0"
	"github.com/hashicorp/go-plugin"
	"os"
	"testing"

	"github.com/codefly-dev/core/configurations"

	"github.com/stretchr/testify/assert"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

func TestCreate(t *testing.T) {
	defer plugin.CleanupClients()
	ctx := context.Background()
	tmpDir := t.TempDir()
	defer func(path string) {
		err := os.RemoveAll(path)
		if err != nil {
			t.Fatal(err)
		}
	}(tmpDir)

	conf := configurations.Service{Name: "svc", Application: "app", Project: "proj"}
	err := conf.SaveAtDir(ctx, tmpDir)
	assert.NoError(t, err)
	identity := &basev0.ServiceIdentity{
		Name:        "svc",
		Application: "app",
		Location:    tmpDir,
	}

	builder := NewBuilder()
	_, err = builder.Load(ctx, &builderv0.LoadRequest{Identity: identity, AtCreate: true})
	assert.NoError(t, err)

}
