package repotest_test

import (
	"testing"

	"github.com/Alexander-D-Karpov/commeng/internal/repository/memory"
	"github.com/Alexander-D-Karpov/commeng/internal/repository/repotest"
)

func TestMemoryRepositoryContract(t *testing.T) {
	repotest.Run(t, memory.New())
}
