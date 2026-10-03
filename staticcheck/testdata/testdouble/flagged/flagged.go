package flagged

type Sink interface {
	Write(message string)
}

type Holder struct {
	Sink Sink
}

func Send(sink Sink) {
	sink.Write("message")
}
