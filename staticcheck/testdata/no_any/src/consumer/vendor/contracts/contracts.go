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

type Scanner[T any] interface{ Scan(T) error }
type Callback[T any] func(T) error
type FixedScanner interface{ Scanner[any] }

func AcceptGeneric[T any](Scanner[T])  {}
func AcceptFixed(Scanner[any])         {}
func HandleFixed(Callback[any])        {}
func HandleGeneric[T any](Callback[T]) {}

type First[T any] interface {
	Build() func(map[any]T, func(any, T))
}

type Second[T comparable] interface {
	Build() func(map[T]any, func(T, any))
}

func AcceptFirst[T any](First[T])          {}
func AcceptSecond[T comparable](Second[T]) {}
