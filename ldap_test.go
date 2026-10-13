package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"
)

type fakeLdapSearcher struct {
	lookupResult   *ldap.SearchResult
	memberOfResult *ldap.SearchResult
	memberResults  map[string]*ldap.SearchResult
	pagingRequests []*ldap.SearchRequest
	pagingSizes    []uint32
	memberRequests []*ldap.SearchRequest
}

func (searcher *fakeLdapSearcher) SearchWithPaging(request *ldap.SearchRequest, pagingSize uint32) (*ldap.SearchResult, error) {
	searcher.pagingRequests = append(searcher.pagingRequests, request)
	searcher.pagingSizes = append(searcher.pagingSizes, pagingSize)
	if strings.Contains(request.Filter, "(memberOf=") {
		if searcher.memberOfResult != nil {
			return searcher.memberOfResult, nil
		}
		return &ldap.SearchResult{}, nil
	}
	return searcher.lookupResult, nil
}

func (searcher *fakeLdapSearcher) Search(request *ldap.SearchRequest) (*ldap.SearchResult, error) {
	searcher.memberRequests = append(searcher.memberRequests, request)
	return searcher.memberResults[request.BaseDN], nil
}

func TestResolveGroupsAndUsersResolvesMemberOutsideBaseDN(t *testing.T) {
	const (
		baseDN   = "OU=Groups,DC=example,DC=com"
		groupDN  = "CN=reviewers,OU=Groups,DC=example,DC=com"
		memberDN = "CN=Jane,OU=People,DC=elsewhere,DC=org"
	)
	group := ldap.NewEntry(groupDN, map[string][]string{
		"objectClass":       {"top", "group"},
		"distinguishedName": {groupDN},
		"sAMAccountName":    {"reviewers"},
		"member":            {memberDN},
	})
	member := ldap.NewEntry(memberDN, map[string][]string{
		"objectClass":    {"top", "person", "user"},
		"sAMAccountName": {"Jane"},
	})
	searcher := &fakeLdapSearcher{
		lookupResult: &ldap.SearchResult{Entries: []*ldap.Entry{group}},
		memberResults: map[string]*ldap.SearchResult{
			memberDN: {Entries: []*ldap.Entry{member}},
		},
	}

	got, err := resolveGroupsAndUsersWithSearcher(searcher, baseDN, []string{"reviewers"})
	if err != nil {
		t.Fatalf("resolveGroupsAndUsersWithSearcher() error = %v", err)
	}
	if want := []string{"Jane"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved users = %#v, want %#v", got, want)
	}
	if len(searcher.memberRequests) != 1 {
		t.Fatalf("member searches = %d, want 1", len(searcher.memberRequests))
	}
	request := searcher.memberRequests[0]
	if request.BaseDN != memberDN || request.Scope != ldap.ScopeBaseObject {
		t.Errorf("member search = (base %q, scope %d), want (base %q, scope %d)", request.BaseDN, request.Scope, memberDN, ldap.ScopeBaseObject)
	}
}

func TestResolveGroupsAndUsersBatchesDirectGroupMembers(t *testing.T) {
	const (
		baseDN  = "OU=People,DC=example,DC=com"
		groupDN = "CN=reviewers,OU=Groups,DC=example,DC=com"
	)
	group := ldap.NewEntry(groupDN, map[string][]string{
		"objectClass":    {"top", "group"},
		"sAMAccountName": {"reviewers"},
		"member": {
			"CN=Jane,OU=People,DC=example,DC=com",
			"CN=John,OU=People,DC=example,DC=com",
		},
	})
	searcher := &fakeLdapSearcher{
		lookupResult: &ldap.SearchResult{Entries: []*ldap.Entry{group}},
		memberOfResult: &ldap.SearchResult{Entries: []*ldap.Entry{
			ldap.NewEntry("CN=Jane,OU=People,DC=example,DC=com", map[string][]string{"sAMAccountName": {"Jane"}}),
			ldap.NewEntry("CN=John,OU=People,DC=example,DC=com", map[string][]string{"sAMAccountName": {"John"}}),
		}},
	}

	got, err := resolveGroupsAndUsersWithSearcher(searcher, baseDN, []string{"reviewers"})
	if err != nil {
		t.Fatalf("resolveGroupsAndUsersWithSearcher() error = %v", err)
	}
	if want := []string{"Jane", "John"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved users = %#v, want %#v", got, want)
	}
	if len(searcher.memberRequests) != 0 {
		t.Errorf("individual member searches = %d, want 0", len(searcher.memberRequests))
	}
	if len(searcher.pagingRequests) != 2 {
		t.Fatalf("paged searches = %d, want group lookup and one member search", len(searcher.pagingRequests))
	}
	memberRequest := searcher.pagingRequests[1]
	if memberRequest.BaseDN != baseDN || memberRequest.Scope != ldap.ScopeWholeSubtree || !strings.Contains(memberRequest.Filter, "(memberOf="+ldap.EscapeFilter(groupDN)+")") {
		t.Errorf("member query = (base %q, scope %d, filter %q), want base %q, subtree scope, and memberOf filter for %q", memberRequest.BaseDN, memberRequest.Scope, memberRequest.Filter, baseDN, groupDN)
	}
	if searcher.pagingSizes[1] != 500 {
		t.Errorf("member query page size = %d, want 500", searcher.pagingSizes[1])
	}
}
