package gc

import (
	"testing"
	"time"

	"github.com/onsi/gomega"
)

func TestDefaultGCConfig(t *testing.T) {
	t.Run("DefaultGCConfig has safe defaults", func(t *testing.T) {
		gm := gomega.NewWithT(t)
		cfg := DefaultGCConfig()
		gm.Expect(cfg.Enabled).To(gomega.BeFalse())
		gm.Expect(cfg.DryRun).To(gomega.BeTrue())
		gm.Expect(cfg.HardTTL).To(gomega.Equal(time.Duration(0)))
		gm.Expect(cfg.SafetyMinCount).To(gomega.Equal(10))
	})

	t.Run("Zero HardTTL means AgentRequest GC is disabled", func(t *testing.T) {
		gm := gomega.NewWithT(t)
		cfg := GCConfig{HardTTL: 0}
		gm.Expect(cfg.HardTTL).To(gomega.Equal(time.Duration(0)))
	})
}
