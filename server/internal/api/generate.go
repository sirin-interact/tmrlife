package api

// 명세를 고친 뒤에는 `make generate`로 api.gen.go를 다시 만든다. 만들어진 파일은 손으로 고치지 않는다.
//go:generate go tool oapi-codegen -config oapi-codegen.yaml ../../openapi.yaml
