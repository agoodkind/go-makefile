package contracts

import "context"

type Handler func(any, context.Context, func(any) error) (any, error)

type Descriptor struct {
	Handler Handler
	Inline  func(any) error
}

type Codec interface {
	Marshal(any) ([]byte, error)
	Unmarshal([]byte, any) error
	Name() string
}

type Factory func() func(any) any

func Register(Codec) {}

func Generic[T any](func(T, any) T) {}

func Nested[T comparable](func(map[T]any, func(T, any)) T) {}

type GenericHandler[T any] func(any, T)

type GenericCodec[T any] interface {
	Encode(any, T) error
}
