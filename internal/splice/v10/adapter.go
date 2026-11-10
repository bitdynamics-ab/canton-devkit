package v10

import (
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
	v06 "github.com/bitdynamics-ab/canton-devkit/internal/splice/v06"
)

type Adapter struct{ *v06.Adapter }

func New() *Adapter { return &Adapter{v06.New()} }

func (*Adapter) MajorVersion() string { return "0.10" }

var _ splice.Adapter = (*Adapter)(nil)
