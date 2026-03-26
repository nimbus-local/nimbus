package services

import "net/http"

type Service interface {
	Name() string
	Detect(r *http.Request) bool
	ServeHTTP(w http.ResponseWriter, r *http.Request)
}
