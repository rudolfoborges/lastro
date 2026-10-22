package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/platformdb"
)

const (
	maxMetadataBytes  = 64 << 10
	maxRegisterBytes  = 16 << 10
	maxRedirectURIs   = 10
	maxClientName     = 200
	fetchTimeout      = 5 * time.Second
	authMethodNone    = "none"
	authMethodPost    = "client_secret_post"
	authMethodBasic   = "client_secret_basic"
	registrationDCR   = "dynamic"
	grantCode         = "authorization_code"
	grantRefresh      = "refresh_token"
	responseTypeCode  = "code"
	contentTypeJSON   = "application/json"
	errInvalidClient  = "invalid_client"
	errInvalidRequest = "invalid_request"
)

var errUnknownClient = errors.New("unknown OAuth client")

type MetadataFetcher interface {
	Fetch(ctx context.Context, documentURL string) ([]byte, error)
}

type clientMetadata struct {
	ClientID                         string   `json:"client_id"`
	ClientName                       string   `json:"client_name"`
	RedirectURIs                     []string `json:"redirect_uris"`
	TokenEndpointAuthMethod          string   `json:"token_endpoint_auth_method"`
	TokenEndpointAuthMethodsAccepted []string `json:"token_endpoint_auth_methods_supported"`
	GrantTypes                       []string `json:"grant_types"`
	ResponseTypes                    []string `json:"response_types"`
}

func (s *Server) client(ctx context.Context, clientID string) (platformdb.OauthClient, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return platformdb.OauthClient{}, errUnknownClient
	}
	if strings.HasPrefix(clientID, "https://") || strings.HasPrefix(clientID, "http://") {
		return s.metadataDocumentClient(ctx, clientID)
	}
	row, err := s.store.Platform().GetOAuthClient(ctx, clientID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.Registration != registrationDCR) {
		return platformdb.OauthClient{}, errUnknownClient
	}
	if err != nil {
		return platformdb.OauthClient{}, fmt.Errorf("get OAuth client: %w", err)
	}
	return row, nil
}

func (s *Server) metadataDocumentClient(ctx context.Context, documentURL string) (platformdb.OauthClient, error) {
	if !s.allowedMetadataURL(documentURL) {
		return platformdb.OauthClient{}, errUnknownClient
	}
	cached, err := s.store.Platform().GetOAuthClient(ctx, documentURL)
	if err == nil && cached.MetadataFetchedAt != nil && s.now().Sub(*cached.MetadataFetchedAt) < metadataTTL {
		return cached, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return platformdb.OauthClient{}, fmt.Errorf("get OAuth client: %w", err)
	}
	body, err := s.cfg.Fetcher.Fetch(ctx, documentURL)
	if err != nil {
		s.logger.WarnContext(ctx, "client metadata document unavailable", "client_id", documentURL, "error", err.Error())
		return platformdb.OauthClient{}, errUnknownClient
	}
	var metadata clientMetadata
	if err := json.Unmarshal(body, &metadata); err != nil {
		return platformdb.OauthClient{}, errUnknownClient
	}
	if metadata.ClientID != documentURL || !s.validRedirects(metadata.RedirectURIs) || !acceptsPublicClient(metadata) {
		return platformdb.OauthClient{}, errUnknownClient
	}
	if len(metadata.GrantTypes) > 0 && !slices.Contains(metadata.GrantTypes, grantCode) {
		return platformdb.OauthClient{}, errUnknownClient
	}
	row, err := s.store.Platform().SaveMetadataDocumentClient(ctx, platformdb.SaveMetadataDocumentClientParams{
		ID:           store.NewID(),
		ClientID:     documentURL,
		ClientName:   clientName(metadata.ClientName),
		RedirectUris: metadata.RedirectURIs,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return platformdb.OauthClient{}, errUnknownClient
	}
	if err != nil {
		return platformdb.OauthClient{}, fmt.Errorf("save client metadata: %w", err)
	}
	return row, nil
}

func acceptsPublicClient(metadata clientMetadata) bool {
	if metadata.TokenEndpointAuthMethod == "" || metadata.TokenEndpointAuthMethod == authMethodNone {
		return true
	}
	return slices.Contains(metadata.TokenEndpointAuthMethodsAccepted, authMethodNone)
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var request struct {
		RedirectURIs            []string `json:"redirect_uris"`
		ClientName              string   `json:"client_name"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
		GrantTypes              []string `json:"grant_types"`
		ResponseTypes           []string `json:"response_types"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRegisterBytes)).Decode(&request); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "the body must be a JSON client registration")
		return
	}
	if !s.validRedirects(request.RedirectURIs) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris must list 1 to 10 HTTPS URIs on an allowed host")
		return
	}
	method := request.TokenEndpointAuthMethod
	if method == "" {
		method = authMethodNone
	}
	if !slices.Contains([]string{authMethodNone, authMethodPost, authMethodBasic}, method) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "token_endpoint_auth_method must be none, client_secret_post or client_secret_basic")
		return
	}
	for _, grant := range request.GrantTypes {
		if grant != grantCode && grant != grantRefresh {
			writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "grant_types may only list authorization_code and refresh_token")
			return
		}
	}
	for _, responseType := range request.ResponseTypes {
		if responseType != responseTypeCode {
			writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "response_types may only list code")
			return
		}
	}

	clientID, _ := opaqueValue(clientIDPrefix)
	params := platformdb.CreateOAuthClientParams{ID: store.NewID(), ClientID: clientID, ClientName: clientName(request.ClientName), RedirectUris: request.RedirectURIs}
	var secret string
	if method != authMethodNone {
		var hash []byte
		secret, hash = opaqueValue(clientSecretPrefix)
		params.SecretHash = hash
	}
	row, err := s.store.Platform().CreateOAuthClient(r.Context(), params)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	response := map[string]any{
		"client_id":                  row.ClientID,
		"client_id_issued_at":        row.CreatedAt.Unix(),
		"redirect_uris":              row.RedirectUris,
		"token_endpoint_auth_method": method,
		"grant_types":                []string{grantCode, grantRefresh},
		"response_types":             []string{responseTypeCode},
	}
	if row.ClientName != nil {
		response["client_name"] = *row.ClientName
	}
	if secret != "" {
		response["client_secret"] = secret
		response["client_secret_expires_at"] = 0
	}
	noStore(w)
	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) validRedirects(uris []string) bool {
	if len(uris) == 0 || len(uris) > maxRedirectURIs {
		return false
	}
	for _, uri := range uris {
		if !s.allowedRedirect(uri) {
			return false
		}
	}
	return true
}

func (s *Server) allowedRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Fragment != "" || u.User != nil || u.Host == "" {
		return false
	}
	if u.Scheme == "https" && (u.Port() == "" || u.Port() == "443") && slices.Contains(s.cfg.RedirectHosts, strings.ToLower(u.Hostname())) {
		return true
	}
	return s.cfg.AllowLoopback && u.Scheme == "http" && isLoopbackHost(u.Hostname())
}

func (s *Server) allowedMetadataURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.Path == "" || u.Path == "/" {
		return false
	}
	if u.Scheme == "https" && (u.Port() == "" || u.Port() == "443") && slices.Contains(s.cfg.MetadataHosts, strings.ToLower(u.Hostname())) {
		return true
	}
	return s.cfg.AllowLoopback && u.Scheme == "http" && isLoopbackHost(u.Hostname())
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func clientName(value string) *string {
	name := strings.TrimSpace(value)
	if name == "" {
		return nil
	}
	if runes := []rune(name); len(runes) > maxClientName {
		name = string(runes[:maxClientName])
	}
	return &name
}

type httpFetcher struct {
	client *http.Client
}

func newHTTPFetcher(allowLoopback bool) *httpFetcher {
	dialer := &net.Dialer{
		Timeout: fetchTimeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || ip.IsUnspecified() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
				return fmt.Errorf("refusing to fetch client metadata from %s", host)
			}
			if ip.IsLoopback() && !allowLoopback {
				return fmt.Errorf("refusing to fetch client metadata from %s", host)
			}
			return nil
		},
	}
	transport := &http.Transport{DialContext: dialer.DialContext, TLSHandshakeTimeout: fetchTimeout, ResponseHeaderTimeout: fetchTimeout}
	return &httpFetcher{client: &http.Client{
		Timeout:       fetchTimeout,
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (f *httpFetcher) Fetch(ctx context.Context, documentURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, documentURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", contentTypeJSON)
	res, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("client metadata answered %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxMetadataBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxMetadataBytes {
		return nil, errors.New("client metadata document is too large")
	}
	return body, nil
}
