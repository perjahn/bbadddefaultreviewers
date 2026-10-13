package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

type ldapSearcher interface {
	SearchWithPaging(searchRequest *ldap.SearchRequest, pagingSize uint32) (*ldap.SearchResult, error)
	Search(searchRequest *ldap.SearchRequest) (*ldap.SearchResult, error)
}

func resolveGroupsAndUsers(serverUrl, baseDN, bindDN, password string, groupsOrUsers []string) ([]string, error) {
	conn, err := ldap.DialURL(serverUrl)
	if err != nil {
		return nil, fmt.Errorf("connect to LDAP: %w", err)
	}
	defer conn.Close()
	conn.SetTimeout(30 * time.Second)
	if bindDN != "" {
		if err := conn.Bind(bindDN, password); err != nil {
			return nil, fmt.Errorf("bind to LDAP: %w", err)
		}
	}

	return resolveGroupsAndUsersWithSearcher(conn, baseDN, groupsOrUsers)
}

func resolveGroupsAndUsersWithSearcher(conn ldapSearcher, baseDN string, groupsOrUsers []string) ([]string, error) {
	users := make([]string, 0)
	for _, groupOrUser := range groupsOrUsers {
		searchValue := ldap.EscapeFilter(groupOrUser)
		lookupFilter := fmt.Sprintf("(&(sAMAccountName=%s)(|(objectCategory=person)(objectClass=group)))", searchValue)
		lookupSearch := ldap.NewSearchRequest(baseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false, lookupFilter, []string{"objectClass", "distinguishedName", "sAMAccountName", "member"}, nil)
		lookupResults, err := conn.SearchWithPaging(lookupSearch, 500)
		if err != nil {
			return nil, fmt.Errorf("find ldap object %q: %w", groupOrUser, err)
		}
		if len(lookupResults.Entries) == 0 {
			return nil, fmt.Errorf("ldap user or group %q was not found under %s", groupOrUser, baseDN)
		}
		if len(lookupResults.Entries) > 1 {
			return nil, fmt.Errorf("ldap object %q is ambiguous; use a unique sAMAccountName", groupOrUser)
		}
		entry := lookupResults.Entries[0]
		if slices.ContainsFunc(entry.GetAttributeValues("objectClass"), func(class string) bool { return strings.EqualFold(class, "group") }) {
			for _, memberDN := range entry.GetAttributeValues("member") {
				memberSearch := ldap.NewSearchRequest(memberDN, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 0, 0, false, "(&(objectCategory=person)(objectClass=user))", []string{"sAMAccountName"}, nil)
				memberResults, err := conn.Search(memberSearch)
				if err != nil {
					return nil, fmt.Errorf("resolve ldap group member %q: %w", memberDN, err)
				}
				for _, member := range memberResults.Entries {
					account := strings.TrimSpace(member.GetAttributeValue("sAMAccountName"))
					if account != "" && !slices.ContainsFunc(users, func(value string) bool { return strings.EqualFold(value, account) }) {
						users = append(users, account)
					}
				}
			}
		} else {
			account := strings.TrimSpace(entry.GetAttributeValue("sAMAccountName"))
			if account != "" && !slices.ContainsFunc(users, func(value string) bool { return strings.EqualFold(value, account) }) {
				users = append(users, account)
			}
		}
	}

	sort.Strings(users)
	return users, nil
}
