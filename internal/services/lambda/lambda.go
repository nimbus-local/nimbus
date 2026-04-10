package lambda

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
	"github.com/nimbus-local/nimbus/internal/services/lambda/aliases"
	"github.com/nimbus-local/nimbus/internal/services/lambda/capacity"
	"github.com/nimbus-local/nimbus/internal/services/lambda/code_signing"
	"github.com/nimbus-local/nimbus/internal/services/lambda/concurrency"
	"github.com/nimbus-local/nimbus/internal/services/lambda/event_sources"
	"github.com/nimbus-local/nimbus/internal/services/lambda/function_crud"
	"github.com/nimbus-local/nimbus/internal/services/lambda/invocation"
	"github.com/nimbus-local/nimbus/internal/services/lambda/layers"
	"github.com/nimbus-local/nimbus/internal/services/lambda/permissions"
	"github.com/nimbus-local/nimbus/internal/services/lambda/settings"
	"github.com/nimbus-local/nimbus/internal/services/lambda/url_config"
)

const defaultAccount = "000000000000"

// Service is the top-level Lambda emulator. It owns routing and composes
// the sub-package services that implement each group of API operations.
type Service struct {
	region       string
	account      string
	CRUD         *function_crud.Service
	Invocation   *invocation.Service
	Permissions  *permissions.Service
	Aliases      *aliases.Service
	EventSources *event_sources.Service
	Concurrency  *concurrency.Service
	Layers       *layers.Service
	CodeSigning  *code_signing.Service
	URLConfig    *url_config.Service
	Settings     *settings.Service
	Tags         *capacity.Service
}

func New(region string) *Service {
	if region == "" {
		region = "us-east-1"
	}
	crud := function_crud.New(region, defaultAccount)
	return &Service{
		region:       region,
		account:      defaultAccount,
		CRUD:         crud,
		Invocation:   invocation.New(crud),
		Permissions:  permissions.New(region, defaultAccount, crud),
		Aliases:      aliases.New(region, defaultAccount, crud),
		EventSources: event_sources.New(crud),
		Concurrency:  concurrency.New(crud),
		Layers:       layers.New(region, defaultAccount),
		CodeSigning:  code_signing.New(region, defaultAccount, crud),
		URLConfig:    url_config.New(region, defaultAccount, crud),
		Settings:     settings.New(crud),
		Tags:         capacity.New(crud),
	}
}

func (s *Service) Name() string { return "lambda" }

// Detect identifies Lambda requests by the /2015-03-31/ path prefix.
func (s *Service) Detect(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/2015-03-31/")
}

// ServeHTTP routes to the appropriate sub-package handler.
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/2015-03-31")

	// Prefix-dispatched routes
	switch {
	case path == "/layers" || strings.HasPrefix(path, "/layers/"):
		s.routeLayers(w, r, path)
		return
	case strings.HasPrefix(path, "/event-source-mappings"):
		s.routeEventSources(w, r, path)
		return
	case path == "/code-signing-configs" || strings.HasPrefix(path, "/code-signing-configs/"):
		s.routeCodeSigning(w, r, path)
		return
	case path == "/account-settings":
		s.Settings.GetAccountSettings(w, r)
		return
	case strings.HasPrefix(path, "/tags/"):
		arn := strings.TrimPrefix(path, "/tags/")
		switch r.Method {
		case http.MethodGet:
			s.Tags.ListTags(w, r, arn)
		case http.MethodPost:
			s.Tags.TagResource(w, r, arn)
		case http.MethodDelete:
			s.Tags.UntagResource(w, r, arn)
		default:
			jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
				"Unknown operation for path: "+r.URL.Path)
		}
		return
	case path == "/event-invoke-config/functions":
		s.URLConfig.ListInvokeConfigs(w, r)
		return
	}

	// Collection-level: /functions
	switch {
	case r.Method == http.MethodPost && path == "/functions":
		s.CRUD.Create(w, r)
		return
	case r.Method == http.MethodGet && path == "/functions":
		s.CRUD.List(w, r)
		return
	}

	// Resource-level: /functions/{name}[/suffix[/arg]]
	rest := strings.TrimPrefix(path, "/functions/")
	if rest == path {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			"Unknown operation for path: "+r.URL.Path)
		return
	}

	name, suffix, _ := strings.Cut(rest, "/")
	if name == "" {
		jsonhttp.Error(w, http.StatusBadRequest, "ValidationException", "FunctionName is required")
		return
	}
	suffix = strings.TrimSuffix(suffix, "/") // normalize invoke-async trailing slash

	// Split suffix further for two-segment paths (e.g. "aliases/myAlias", "policy/mySid").
	suffixBase, suffixArg, _ := strings.Cut(suffix, "/")

	switch {
	// CRUD
	case r.Method == http.MethodGet && suffix == "":
		s.CRUD.Get(w, r, name)
	case r.Method == http.MethodGet && suffix == "configuration":
		s.CRUD.GetConfiguration(w, r, name)
	case r.Method == http.MethodPut && suffix == "code":
		s.CRUD.UpdateCode(w, r, name)
	case r.Method == http.MethodPut && suffix == "configuration":
		s.CRUD.UpdateConfiguration(w, r, name)
	case r.Method == http.MethodDelete && suffix == "":
		s.CRUD.Delete(w, r, name)
	case r.Method == http.MethodGet && suffix == "versions":
		s.CRUD.ListVersions(w, r, name)
	case r.Method == http.MethodPost && suffix == "versions":
		s.CRUD.PublishVersion(w, r, name)

	// Invocation
	case r.Method == http.MethodPost && suffix == "invocations":
		s.Invocation.Invoke(w, r, name)
	case r.Method == http.MethodPost && suffix == "invoke-async":
		s.Invocation.InvokeAsync(w, r, name)
	case r.Method == http.MethodPost && suffix == "response-streaming-invocations":
		s.Invocation.InvokeWithResponseStream(w, r, name)

	// Permissions (resource-based policy)
	case r.Method == http.MethodPost && suffix == "policy":
		s.Permissions.AddPermission(w, r, name)
	case r.Method == http.MethodGet && suffix == "policy":
		s.Permissions.GetPolicy(w, r, name)
	case r.Method == http.MethodDelete && suffixBase == "policy" && suffixArg != "":
		s.Permissions.RemovePermission(w, r, name, suffixArg)

	// Aliases
	case r.Method == http.MethodPost && suffix == "aliases":
		s.Aliases.Create(w, r, name)
	case r.Method == http.MethodGet && suffix == "aliases":
		s.Aliases.List(w, r, name)
	case r.Method == http.MethodGet && suffixBase == "aliases" && suffixArg != "":
		s.Aliases.Get(w, r, name, suffixArg)
	case r.Method == http.MethodPut && suffixBase == "aliases" && suffixArg != "":
		s.Aliases.Update(w, r, name, suffixArg)
	case r.Method == http.MethodDelete && suffixBase == "aliases" && suffixArg != "":
		s.Aliases.Delete(w, r, name, suffixArg)

	// Concurrency
	case r.Method == http.MethodPut && suffix == "concurrency":
		s.Concurrency.Put(w, r, name)
	case r.Method == http.MethodGet && suffix == "concurrency":
		s.Concurrency.Get(w, r, name)
	case r.Method == http.MethodDelete && suffix == "concurrency":
		s.Concurrency.Delete(w, r, name)
	case r.Method == http.MethodPut && suffix == "provisioned-concurrency":
		s.Concurrency.PutProvisioned(w, r, name)
	case r.Method == http.MethodGet && suffix == "provisioned-concurrency":
		// GetProvisionedConcurrencyConfig requires ?Qualifier=; List does not.
		if r.URL.Query().Get("Qualifier") != "" {
			s.Concurrency.GetProvisioned(w, r, name)
		} else {
			s.Concurrency.ListProvisioned(w, r, name)
		}
	case r.Method == http.MethodDelete && suffix == "provisioned-concurrency":
		s.Concurrency.DeleteProvisioned(w, r, name)

	// Code Signing (function-scoped)
	case r.Method == http.MethodPut && suffix == "code-signing-config":
		s.CodeSigning.PutFunctionConfig(w, r, name)
	case r.Method == http.MethodGet && suffix == "code-signing-config":
		s.CodeSigning.GetFunctionConfig(w, r, name)
	case r.Method == http.MethodDelete && suffix == "code-signing-config":
		s.CodeSigning.DeleteFunctionConfig(w, r, name)

	// Function URLs
	case r.Method == http.MethodPost && suffix == "url":
		s.URLConfig.CreateUrl(w, r, name)
	case r.Method == http.MethodGet && suffix == "url":
		s.URLConfig.GetUrl(w, r, name)
	case r.Method == http.MethodPut && suffix == "url":
		s.URLConfig.UpdateUrl(w, r, name)
	case r.Method == http.MethodDelete && suffix == "url":
		s.URLConfig.DeleteUrl(w, r, name)
	case r.Method == http.MethodGet && suffix == "urls":
		s.URLConfig.ListUrls(w, r, name)

	// Event Invoke Config
	case r.Method == http.MethodPut && suffix == "event-invoke-config":
		s.URLConfig.PutInvokeConfig(w, r, name)
	case r.Method == http.MethodGet && suffix == "event-invoke-config":
		s.URLConfig.GetInvokeConfig(w, r, name)
	case r.Method == http.MethodPost && suffix == "event-invoke-config":
		s.URLConfig.UpdateInvokeConfig(w, r, name)
	case r.Method == http.MethodDelete && suffix == "event-invoke-config":
		s.URLConfig.DeleteInvokeConfig(w, r, name)

	// Runtime & Recursion settings
	case r.Method == http.MethodGet && suffix == "runtime-management-config":
		s.Settings.GetRuntimeConfig(w, r, name)
	case r.Method == http.MethodPut && suffix == "runtime-management-config":
		s.Settings.PutRuntimeConfig(w, r, name)
	case r.Method == http.MethodPut && suffix == "recursion-config":
		s.Settings.PutRecursionConfig(w, r, name)
	case r.Method == http.MethodGet && suffix == "recursion-config":
		s.Settings.GetRecursionConfig(w, r, name)

	default:
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			"Unknown operation for path: "+r.URL.Path)
	}
}

// routeEventSources handles /event-source-mappings[/{uuid}]
func (s *Service) routeEventSources(w http.ResponseWriter, r *http.Request, path string) {
	rest := strings.TrimPrefix(path, "/event-source-mappings")
	rest = strings.TrimPrefix(rest, "/")
	uuid := strings.TrimSuffix(rest, "/")

	switch {
	case r.Method == http.MethodPost && uuid == "":
		s.EventSources.Create(w, r)
	case r.Method == http.MethodGet && uuid == "":
		s.EventSources.List(w, r)
	case r.Method == http.MethodGet && uuid != "":
		s.EventSources.Get(w, r, uuid)
	case r.Method == http.MethodPut && uuid != "":
		s.EventSources.Update(w, r, uuid)
	case r.Method == http.MethodDelete && uuid != "":
		s.EventSources.Delete(w, r, uuid)
	default:
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			"Unknown operation for path: "+r.URL.Path)
	}
}

// routeCodeSigning handles /code-signing-configs[/{arn}[/functions]]
func (s *Service) routeCodeSigning(w http.ResponseWriter, r *http.Request, path string) {
	rest := strings.TrimPrefix(path, "/code-signing-configs")
	rest = strings.TrimPrefix(rest, "/")

	if rest == "" {
		switch r.Method {
		case http.MethodPost:
			s.CodeSigning.Create(w, r)
		case http.MethodGet:
			s.CodeSigning.ListConfigs(w, r)
		default:
			jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
				"Unknown operation for path: "+r.URL.Path)
		}
		return
	}

	// rest is "{arn}" or "{arn}/functions"
	arn, suffix, _ := strings.Cut(rest, "/")
	switch {
	case r.Method == http.MethodGet && suffix == "":
		s.CodeSigning.GetConfig(w, r, arn)
	case r.Method == http.MethodPut && suffix == "":
		s.CodeSigning.UpdateConfig(w, r, arn)
	case r.Method == http.MethodDelete && suffix == "":
		s.CodeSigning.DeleteConfig(w, r, arn)
	case r.Method == http.MethodGet && suffix == "functions":
		s.CodeSigning.ListFunctionsByConfig(w, r, arn)
	default:
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			"Unknown operation for path: "+r.URL.Path)
	}
}

// routeLayers handles /layers and /layers/{layerName}/versions[/{n}[/policy[/{sid}]]]
func (s *Service) routeLayers(w http.ResponseWriter, r *http.Request, path string) {
	if path == "/layers" {
		if r.Method == http.MethodGet {
			s.Layers.ListLayers(w, r)
		} else {
			jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
				"Unknown operation for path: "+r.URL.Path)
		}
		return
	}

	// /layers/{layerName}/versions[/{n}[/policy[/{sid}]]]
	rest := strings.TrimPrefix(path, "/layers/")
	layerName, rest, ok := strings.Cut(rest, "/versions")
	if !ok || layerName == "" {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			"Unknown operation for path: "+r.URL.Path)
		return
	}

	// rest is "" (just /versions) or "/{n}..." or ""
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		// /layers/{name}/versions
		switch r.Method {
		case http.MethodPost:
			s.Layers.Publish(w, r, layerName)
		case http.MethodGet:
			s.Layers.ListVersions(w, r, layerName)
		default:
			jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
				"Unknown operation for path: "+r.URL.Path)
		}
		return
	}

	// /layers/{name}/versions/{n}[/policy[/{sid}]]
	versionStr, rest, _ := strings.Cut(rest, "/")
	version, err := strconv.ParseInt(versionStr, 10, 64)
	if err != nil {
		jsonhttp.Error(w, http.StatusBadRequest, "ValidationException", "invalid version number")
		return
	}

	if rest == "" {
		// /layers/{name}/versions/{n}
		switch r.Method {
		case http.MethodGet:
			s.Layers.GetVersion(w, r, layerName, version)
		case http.MethodDelete:
			s.Layers.DeleteVersion(w, r, layerName, version)
		default:
			jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
				"Unknown operation for path: "+r.URL.Path)
		}
		return
	}

	// /layers/{name}/versions/{n}/policy[/{sid}]
	policyBase, statementId, _ := strings.Cut(rest, "/")
	if policyBase != "policy" {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			"Unknown operation for path: "+r.URL.Path)
		return
	}
	switch {
	case r.Method == http.MethodPost && statementId == "":
		s.Permissions.AddLayerVersionPermission(w, r, layerName, int(version))
	case r.Method == http.MethodGet && statementId == "":
		s.Permissions.GetLayerVersionPolicy(w, r, layerName, int(version))
	case r.Method == http.MethodDelete && statementId != "":
		s.Permissions.RemoveLayerVersionPermission(w, r, layerName, int(version), statementId)
	default:
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			"Unknown operation for path: "+r.URL.Path)
	}
}
