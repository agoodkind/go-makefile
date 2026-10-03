package clean

type Sink interface {
	Write(message string)
}

type Production struct{}

func (Production) Write(string) {}

func Send(sink Sink) {
	sink.Write("message")
}
