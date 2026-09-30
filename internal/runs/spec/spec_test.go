package spec_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/lezhnev74/sandboxed_agent/internal/runs/spec"
)

func TestDefaults(t *testing.T) {
	assert.Equal(t, spec.Spec{Docker: true, Timeout: 60 * time.Minute}, spec.Default())
}
