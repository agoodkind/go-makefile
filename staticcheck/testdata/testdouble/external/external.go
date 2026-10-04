package external

type Sink interface {
	Write(message string)
}

func Send(sink Sink) {
	sink.Write("message")
}
