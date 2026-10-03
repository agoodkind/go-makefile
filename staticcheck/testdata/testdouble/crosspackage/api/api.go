package api

type Store interface {
	Load(key string) (string, error)
}
