package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

type directoryConnectionTarget struct {
	Scheme        string
	Host          string
	RequestedHost string
	ConnectHost   string
	Port          int
	ServerURL     string
}

func directoryTestProvider(ctx context.Context, secret authSecretService, provider directoryProviderConfig) (bool, string) {
	conn, err := directoryServiceConnection(ctx, secret, provider)
	if err != nil {
		return false, err.Error()
	}
	defer conn.Close()
	baseDN := strings.TrimSpace(nullString(provider.Row.BaseDN))
	if baseDN != "" {
		req := ldap.NewSearchRequest(baseDN, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 10, false, "(objectClass=*)", []string{"distinguishedName"}, nil)
		if _, err := conn.Search(req); err != nil {
			return false, err.Error()
		}
	}
	return true, "Provider connectivity verified."
}

func directoryServiceConnection(ctx context.Context, secret authSecretService, provider directoryProviderConfig) (*ldap.Conn, error) {
	urls := provider.serverURLs()
	if len(urls) == 0 {
		return nil, newDirectoryError("missing_server", "Provider has no LDAP server URL.", http.StatusBadRequest)
	}
	bindDN := strings.TrimSpace(nullString(provider.Row.BindDN))
	bindPassword, err := decryptDirectorySecret(ctx, secret, nullString(provider.Row.BindPasswordEncrypted))
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, serverURL := range urls {
		target, err := directoryConnectionTargetFor(provider, serverURL)
		if err != nil {
			return nil, err
		}
		if sqlIntBool(provider.Row.TLSRequired) && target.Scheme != "ldaps" {
			lastErr = fmt.Errorf("Strict TLS requires ldaps:// server URLs.")
			continue
		}
		conn, err := dialDirectoryTarget(ctx, provider, target)
		if err != nil {
			lastErr = err
			continue
		}
		if bindDN != "" || bindPassword != "" {
			if err := conn.Bind(bindDN, bindPassword); err != nil {
				conn.Close()
				lastErr = err
				continue
			}
		} else if err := conn.UnauthenticatedBind(""); err != nil {
			conn.Close()
			lastErr = err
			continue
		}
		return conn, nil
	}
	return nil, newDirectoryError("ldap_connect_failed", firstText(errorText(lastErr), "LDAP connection failed."), http.StatusBadGateway)
}

func directorySearchUser(ctx context.Context, secret authSecretService, provider directoryProviderConfig, loginName string) (directoryUserInfo, bool, error) {
	baseDN := strings.TrimSpace(nullString(provider.Row.BaseDN))
	if baseDN == "" {
		return directoryUserInfo{}, false, nil
	}
	conn, err := directoryServiceConnection(ctx, secret, provider)
	if err != nil {
		return directoryUserInfo{}, false, err
	}
	defer conn.Close()
	usernameAttr := firstText(nullString(provider.Row.UsernameAttribute), provider.defaultUsernameAttribute())
	displayAttr := firstText(nullString(provider.Row.DisplayNameAttribute), "displayName")
	emailAttr := firstText(nullString(provider.Row.EmailAttribute), "mail")
	memberAttr := firstText(nullString(provider.Row.MemberOfAttribute), "memberOf")
	accountName, _ := directoryDomainHint(loginName)
	escapedLogin := ldap.EscapeFilter(strings.TrimSpace(loginName))
	escapedAccount := ldap.EscapeFilter(strings.TrimSpace(accountName))
	filterTemplate := strings.TrimSpace(nullString(provider.Row.UserSearchFilter))
	searchFilter := ""
	if filterTemplate != "" {
		searchFilter = strings.NewReplacer(
			"{username}", escapedAccount,
			"{login}", escapedLogin,
			"{user}", escapedAccount,
		).Replace(filterTemplate)
	} else if provider.providerType() == "active_directory" {
		searchFilter = fmt.Sprintf("(|(sAMAccountName=%s)(userPrincipalName=%s)(%s=%s))", escapedAccount, escapedLogin, emailAttr, escapedLogin)
	} else {
		searchFilter = fmt.Sprintf("(%s=%s)", usernameAttr, firstText(escapedAccount, escapedLogin))
	}
	attrs := uniqueDirectoryAttrs([]string{usernameAttr, "sAMAccountName", "userPrincipalName", displayAttr, emailAttr, memberAttr, "distinguishedName", "objectGUID", "entryUUID", "objectSid", "cn"})
	req := ldap.NewSearchRequest(baseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 10, false, searchFilter, attrs, nil)
	result, err := conn.Search(req)
	if err != nil {
		return directoryUserInfo{}, false, err
	}
	if len(result.Entries) != 1 {
		return directoryUserInfo{}, false, nil
	}
	entry := result.Entries[0]
	attrMap := directoryEntryAttrs(entry)
	dn := firstText(strings.TrimSpace(entry.DN), firstDirectoryAttr(attrMap, "distinguishedName"))
	groups := directoryAttrList(attrMap, memberAttr)
	if sqlIntBool(provider.Row.NestedGroups) && strings.TrimSpace(nullString(provider.Row.GroupSearchBaseDN)) != "" && dn != "" {
		groups = cleanStringSlice(append(groups, directorySearchNestedGroups(conn, provider, dn)...))
	}
	account := firstText(firstDirectoryAttr(attrMap, "userPrincipalName"), firstDirectoryAttr(attrMap, usernameAttr), firstDirectoryAttr(attrMap, "sAMAccountName"), firstDirectoryAttr(attrMap, emailAttr), loginName)
	displayName := firstText(firstDirectoryAttr(attrMap, displayAttr), firstDirectoryAttr(attrMap, "cn"), firstDirectoryAttr(attrMap, usernameAttr), firstDirectoryAttr(attrMap, "sAMAccountName"), account)
	subject := firstText(firstDirectoryAttr(attrMap, "entryUUID"), firstDirectoryAttr(attrMap, "objectGUID"), firstDirectoryAttr(attrMap, "objectSid"), dn, account)
	return directoryUserInfo{DN: dn, Account: account, DisplayName: displayName, Subject: subject, Groups: groups, Attrs: attrMap}, true, nil
}

func directoryVerifyPassword(ctx context.Context, secret authSecretService, provider directoryProviderConfig, userInfo directoryUserInfo, loginName string, password string) error {
	if strings.TrimSpace(password) == "" {
		return newDirectoryError("missing_credentials", "Directory username and password are required.", http.StatusBadRequest)
	}
	bindUser := strings.TrimSpace(userInfo.DN)
	if provider.providerType() == "active_directory" {
		bindUser = firstText(userInfo.Account, userInfo.DN, loginName)
	}
	if bindUser == "" {
		return newDirectoryError("directory_user_not_found", "Directory user DN was not found.", http.StatusUnauthorized)
	}
	urls := provider.serverURLs()
	var lastErr error
	for _, serverURL := range urls {
		target, err := directoryConnectionTargetFor(provider, serverURL)
		if err != nil {
			return err
		}
		if sqlIntBool(provider.Row.TLSRequired) && target.Scheme != "ldaps" {
			lastErr = fmt.Errorf("Strict TLS requires ldaps:// server URLs.")
			continue
		}
		conn, err := dialDirectoryTarget(ctx, provider, target)
		if err != nil {
			lastErr = err
			continue
		}
		err = conn.Bind(bindUser, password)
		conn.Close()
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return newDirectoryError("invalid_username_or_password", firstText(errorText(lastErr), "LDAP bind failed."), http.StatusUnauthorized)
}

func directoryPreviewGroupAccess(ctx context.Context, secret authSecretService, provider directoryProviderConfig, groupDNS []string) ([]map[string]any, error) {
	baseDN := strings.TrimSpace(nullString(provider.Row.BaseDN))
	groupDNS = cleanStringSlice(groupDNS)
	if baseDN == "" || len(groupDNS) == 0 {
		return []map[string]any{}, nil
	}
	conn, err := directoryServiceConnection(ctx, secret, provider)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	usernameAttr := firstText(nullString(provider.Row.UsernameAttribute), provider.defaultUsernameAttribute())
	displayAttr := firstText(nullString(provider.Row.DisplayNameAttribute), "displayName")
	emailAttr := firstText(nullString(provider.Row.EmailAttribute), "mail")
	memberAttr := firstText(nullString(provider.Row.MemberOfAttribute), "memberOf")
	attrs := uniqueDirectoryAttrs([]string{usernameAttr, "sAMAccountName", "userPrincipalName", displayAttr, emailAttr, memberAttr, "distinguishedName", "objectGUID", "cn"})
	byDN := map[string]map[string]any{}
	for _, groupDN := range groupDNS {
		escaped := ldap.EscapeFilter(groupDN)
		memberFilter := fmt.Sprintf("(%s=%s)", memberAttr, escaped)
		if sqlIntBool(provider.Row.NestedGroups) {
			memberFilter = fmt.Sprintf("(%s:1.2.840.113556.1.4.1941:=%s)", memberAttr, escaped)
		}
		searchFilter := fmt.Sprintf("(&(|(objectClass=user)(objectClass=person))(objectClass=*)%s)", memberFilter)
		req := ldap.NewSearchRequest(baseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 10, false, searchFilter, attrs, nil)
		result, err := conn.Search(req)
		if err != nil {
			continue
		}
		for _, entry := range result.Entries {
			attrMap := directoryEntryAttrs(entry)
			dn := firstText(strings.TrimSpace(entry.DN), firstDirectoryAttr(attrMap, "distinguishedName"))
			if dn == "" {
				continue
			}
			key := strings.ToLower(dn)
			row := byDN[key]
			if row == nil {
				row = map[string]any{
					"dn":             dn,
					"account":        firstText(firstDirectoryAttr(attrMap, "userPrincipalName"), firstDirectoryAttr(attrMap, usernameAttr), firstDirectoryAttr(attrMap, "sAMAccountName"), firstDirectoryAttr(attrMap, emailAttr), dn),
					"display_name":   firstText(firstDirectoryAttr(attrMap, displayAttr), firstDirectoryAttr(attrMap, "cn"), firstDirectoryAttr(attrMap, usernameAttr), firstDirectoryAttr(attrMap, "sAMAccountName"), dn),
					"matched_groups": []string{},
				}
				byDN[key] = row
			}
			row["matched_groups"] = appendMissingString(row["matched_groups"], groupDN)
		}
	}
	users := make([]map[string]any, 0, len(byDN))
	for _, row := range byDN {
		users = append(users, row)
	}
	return users, nil
}

func directorySearchNestedGroups(conn *ldap.Conn, provider directoryProviderConfig, userDN string) []string {
	baseDN := strings.TrimSpace(nullString(provider.Row.GroupSearchBaseDN))
	if baseDN == "" {
		return []string{}
	}
	searchFilter := fmt.Sprintf("(member:1.2.840.113556.1.4.1941:=%s)", ldap.EscapeFilter(userDN))
	req := ldap.NewSearchRequest(baseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 10, false, searchFilter, []string{"distinguishedName", "cn"}, nil)
	result, err := conn.Search(req)
	if err != nil {
		return []string{}
	}
	groups := []string{}
	for _, entry := range result.Entries {
		attrMap := directoryEntryAttrs(entry)
		dn := firstText(strings.TrimSpace(entry.DN), firstDirectoryAttr(attrMap, "distinguishedName"))
		if dn != "" {
			groups = append(groups, dn)
		}
	}
	return groups
}

func dialDirectoryTarget(ctx context.Context, provider directoryProviderConfig, target directoryConnectionTarget) (*ldap.Conn, error) {
	tlsConfig, err := directoryTLSConfig(provider, target)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if deadline, ok := ctx.Deadline(); ok {
		timeout := time.Until(deadline)
		if timeout > 0 && timeout < dialer.Timeout {
			dialer.Timeout = timeout
		}
	}
	opts := []ldap.DialOpt{ldap.DialWithDialer(dialer)}
	if tlsConfig != nil {
		opts = append(opts, ldap.DialWithTLSConfig(tlsConfig))
	}
	return ldap.DialURL(target.Scheme+"://"+net.JoinHostPort(target.ConnectHost, fmt.Sprint(target.Port)), opts...)
}

func directoryTLSConfig(provider directoryProviderConfig, target directoryConnectionTarget) (*tls.Config, error) {
	if target.Scheme != "ldaps" {
		return nil, nil
	}
	serverName := firstText(target.Host, target.RequestedHost)
	caPEM := strings.TrimSpace(nullString(provider.Row.TLSCAPEM))
	if caPEM != "" && pemContainsPinnedLeaf(caPEM) {
		if err := verifyPinnedLDAPSDirectoryCertificate(target.ServerURL, caPEM, provider.hostOverrides()); err != nil {
			return nil, err
		}
		return &tls.Config{ServerName: serverName, InsecureSkipVerify: true}, nil
	}
	config := &tls.Config{ServerName: serverName}
	if caPEM != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(caPEM)) {
			return nil, newDirectoryError("invalid_certificate", "TLS CA PEM did not contain a valid certificate.", http.StatusBadRequest)
		}
		config.RootCAs = pool
	}
	if !sqlIntBool(provider.Row.TLSRequired) && caPEM == "" {
		config.InsecureSkipVerify = true
	}
	return config, nil
}

func decryptDirectorySecret(ctx context.Context, secret authSecretService, encrypted string) (string, error) {
	encrypted = strings.TrimSpace(encrypted)
	if encrypted == "" {
		return "", nil
	}
	if secret == nil {
		return "", errAegisLocked
	}
	return secret.decryptSecretText(ctx, encrypted)
}

func directoryConnectionTargetFor(provider directoryProviderConfig, serverURL string) (directoryConnectionTarget, error) {
	scheme, host, port, _, err := parseDirectoryLDAPURL(serverURL, defaultDirectoryScheme(provider))
	if err != nil {
		return directoryConnectionTarget{}, err
	}
	connectHost, tlsHost := resolveDirectoryHostOverride(host, provider.hostOverrides())
	if connectHost == "" {
		connectHost = host
	}
	if tlsHost == "" {
		tlsHost = host
	}
	return directoryConnectionTarget{
		Scheme:        scheme,
		Host:          tlsHost,
		RequestedHost: host,
		ConnectHost:   connectHost,
		Port:          port,
		ServerURL:     scheme + "://" + net.JoinHostPort(tlsHost, fmt.Sprint(port)),
	}, nil
}

func parseDirectoryLDAPURL(serverURL string, defaultScheme string) (string, string, int, string, error) {
	text := strings.TrimSpace(serverURL)
	if text == "" {
		return "", "", 0, "", newDirectoryError("missing_server", "LDAP server URL is required.", http.StatusBadRequest)
	}
	if !strings.Contains(text, "://") {
		text = firstText(defaultScheme, "ldap") + "://" + text
	}
	parsed, err := url.Parse(text)
	if err != nil {
		return "", "", 0, "", newDirectoryError("invalid_server_url", "LDAP server URL must use ldap:// or ldaps://.", http.StatusBadRequest)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "ldap" && scheme != "ldaps" {
		return "", "", 0, "", newDirectoryError("invalid_server_url", "LDAP server URL must use ldap:// or ldaps://.", http.StatusBadRequest)
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return "", "", 0, "", newDirectoryError("invalid_server_url", "LDAP server URL is missing a host.", http.StatusBadRequest)
	}
	port := 389
	if scheme == "ldaps" {
		port = 636
	}
	if parsed.Port() != "" {
		parsedPort, err := strconvAtoi(parsed.Port())
		if err != nil || parsedPort <= 0 || parsedPort > 65535 {
			return "", "", 0, "", newDirectoryError("invalid_server_url", "LDAP server URL has an invalid port.", http.StatusBadRequest)
		}
		port = parsedPort
	}
	normalized := scheme + "://" + net.JoinHostPort(host, fmt.Sprint(port))
	return scheme, host, port, normalized, nil
}

func fetchLDAPSDirectoryCertificate(serverURL string, hostOverrides map[string]string) (map[string]any, error) {
	scheme, host, port, _, err := parseDirectoryLDAPURL(serverURL, "ldaps")
	if err != nil {
		return nil, err
	}
	if scheme != "ldaps" {
		return nil, newDirectoryError("ldaps_required", "Certificate download requires an LDAPS server URL.", http.StatusBadRequest)
	}
	connectHost, tlsHost := resolveDirectoryHostOverride(host, hostOverrides)
	if connectHost == "" {
		connectHost = host
	}
	if tlsHost == "" {
		tlsHost = host
	}
	config := &tls.Config{ServerName: tlsHost, InsecureSkipVerify: true}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", net.JoinHostPort(connectHost, fmt.Sprint(port)), config)
	if err != nil {
		return nil, newDirectoryError("certificate_download_failed", err.Error(), http.StatusBadGateway)
	}
	defer conn.Close()
	if len(conn.ConnectionState().PeerCertificates) == 0 {
		return nil, newDirectoryError("certificate_download_failed", "LDAPS server did not present a certificate.", http.StatusBadGateway)
	}
	return directoryCertificateMetadata(conn.ConnectionState().PeerCertificates[0], scheme+"://"+net.JoinHostPort(tlsHost, fmt.Sprint(port)), tlsHost, connectHost, host, port), nil
}

func verifyPinnedLDAPSDirectoryCertificate(serverURL string, pemText string, hostOverrides map[string]string) error {
	expected := map[string]bool{}
	for _, cert := range pemCertificates(pemText) {
		if !certificateIsCA(cert) {
			expected[certificateFingerprint(cert)] = true
		}
	}
	if len(expected) == 0 {
		return nil
	}
	certificate, err := fetchLDAPSDirectoryCertificate(serverURL, hostOverrides)
	if err != nil {
		return err
	}
	if !expected[cleanText(certificate["sha256_fingerprint"])] {
		return newDirectoryError("pinned_certificate_mismatch", "LDAPS certificate does not match the trusted certificate pinned for this provider.", http.StatusBadGateway)
	}
	return nil
}

func directoryCertificateMetadata(cert *x509.Certificate, serverURL string, host string, connectHost string, requestedHost string, port int) map[string]any {
	dnsNames := append([]string{}, cert.DNSNames...)
	ipAddresses := make([]string, 0, len(cert.IPAddresses))
	for _, ip := range cert.IPAddresses {
		ipAddresses = append(ipAddresses, ip.String())
	}
	return map[string]any{
		"server_url":         serverURL,
		"host":               host,
		"port":               port,
		"subject":            cert.Subject.String(),
		"issuer":             cert.Issuer.String(),
		"common_name":        cert.Subject.CommonName,
		"serial_number":      strings.ToUpper(cert.SerialNumber.Text(16)),
		"sha256_fingerprint": certificateFingerprint(cert),
		"not_before":         cert.NotBefore.UTC().Format(time.RFC3339),
		"not_after":          cert.NotAfter.UTC().Format(time.RFC3339),
		"dns_names":          dnsNames,
		"ip_addresses":       ipAddresses,
		"pem":                string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})),
		"connect_host":       connectHost,
		"requested_host":     requestedHost,
	}
}

func certificateFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	raw := strings.ToUpper(hex.EncodeToString(sum[:]))
	parts := make([]string, 0, len(raw)/2)
	for i := 0; i+2 <= len(raw); i += 2 {
		parts = append(parts, raw[i:i+2])
	}
	return strings.Join(parts, ":")
}

func pemContainsPinnedLeaf(pemText string) bool {
	for _, cert := range pemCertificates(pemText) {
		if !certificateIsCA(cert) {
			return true
		}
	}
	return false
}

func pemCertificates(pemText string) []*x509.Certificate {
	certs := []*x509.Certificate{}
	rest := []byte(pemText)
	for {
		block, remaining := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = remaining
		if block.Type != "CERTIFICATE" {
			continue
		}
		if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
			certs = append(certs, cert)
		}
	}
	return certs
}

func certificateIsCA(cert *x509.Certificate) bool {
	return cert != nil && cert.IsCA
}

func defaultDirectoryScheme(provider directoryProviderConfig) string {
	if sqlIntBool(provider.Row.UseLDAPS) {
		return "ldaps"
	}
	return "ldap"
}

func splitDirectoryServerURLs(value any, useLDAPS bool) []string {
	scheme := "ldap"
	if useLDAPS {
		scheme = "ldaps"
	}
	items := directoryStringSliceFromAny(value)
	urls := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.Contains(item, "://") {
			urls = append(urls, item)
		} else {
			urls = append(urls, scheme+"://"+item)
		}
	}
	return urls
}

func splitDirectoryHostOverrides(value any) map[string]string {
	result := map[string]string{}
	switch typed := value.(type) {
	case map[string]string:
		for key, item := range typed {
			key = strings.ToLower(strings.TrimSpace(key))
			item = strings.TrimSpace(item)
			if key != "" && item != "" {
				result[key] = item
			}
		}
	case map[string]any:
		for key, item := range typed {
			key = strings.ToLower(strings.TrimSpace(key))
			text := cleanText(item)
			if key != "" && text != "" {
				result[key] = text
			}
		}
	case string:
		for _, line := range strings.FieldsFunc(typed, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := []string{}
			if strings.Contains(line, "=") {
				parts = strings.SplitN(line, "=", 2)
			} else if strings.Contains(line, "|") {
				parts = strings.SplitN(line, "|", 2)
			} else {
				parts = strings.Fields(line)
			}
			if len(parts) == 2 {
				host := strings.ToLower(strings.TrimSpace(parts[0]))
				target := strings.TrimSpace(parts[1])
				if host != "" && target != "" {
					result[host] = target
				}
			}
		}
	}
	return result
}

func resolveDirectoryHostOverride(host string, overrides map[string]string) (string, string) {
	hostName := strings.ToLower(strings.TrimSpace(host))
	if hostName == "" {
		return "", ""
	}
	normalized := splitDirectoryHostOverrides(overrides)
	if value := normalized[hostName]; value != "" {
		return value, hostName
	}
	shortName := strings.SplitN(hostName, ".", 2)[0]
	if !strings.Contains(hostName, ".") {
		for overrideHost, connectHost := range normalized {
			if strings.SplitN(overrideHost, ".", 2)[0] == shortName {
				return connectHost, overrideHost
			}
		}
	} else if value := normalized[shortName]; value != "" {
		return value, hostName
	}
	return hostName, hostName
}

func uniqueDirectoryAttrs(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func directoryEntryAttrs(entry *ldap.Entry) map[string][]string {
	result := map[string][]string{}
	if entry == nil {
		return result
	}
	for _, attr := range entry.Attributes {
		if attr == nil {
			continue
		}
		result[strings.ToLower(strings.TrimSpace(attr.Name))] = append([]string{}, attr.Values...)
	}
	return result
}

func firstDirectoryAttr(attrs map[string][]string, names ...string) string {
	for _, name := range names {
		values := directoryAttrList(attrs, name)
		if len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func directoryAttrList(attrs map[string][]string, name string) []string {
	values := attrs[strings.ToLower(strings.TrimSpace(name))]
	return cleanStringSlice(values)
}

func appendMissingString(value any, item string) []string {
	items := directoryStringSliceFromAny(value)
	for _, existing := range items {
		if existing == item {
			return items
		}
	}
	return append(items, item)
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func strconvAtoi(value string) (int, error) {
	value = strings.TrimSpace(value)
	n := 0
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("invalid integer")
		}
		n = n*10 + int(ch-'0')
	}
	return n, nil
}
