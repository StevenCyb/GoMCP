module github.com/StevenCyb/example_esm

go 1.26.2

require (
	github.com/StevenCyb/GoMCP v0.0.0
	github.com/coreos/go-oidc/v3 v3.21.0
	github.com/go-jose/go-jose/v4 v4.1.4
	github.com/stretchr/testify v1.12.1
)

require (
	github.com/stretchr/objx v0.5.3 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
)

replace github.com/StevenCyb/GoMCP => ../..
