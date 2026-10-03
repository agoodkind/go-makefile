package user

import "example.com/testdouble/crosspackage/api"

func Name(store api.Store) (string, error) {
	return store.Load("name")
}
