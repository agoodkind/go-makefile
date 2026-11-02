package local

type Handler func(any) error

type Scanner interface {
	Scan(any) error
}

func Callback(func(any) error) {}

func Register(Scanner) {}
