package main

import (
	"reflect"
	"testing"

	"github.com/go-ldap/ldap/v3"
)

type fakeLdapSearcher struct {
	lookupResult   *ldap.SearchResult
	memberResults  map[string]*ldap.SearchResult
	memberRequests []*ldap.SearchRequest
}

func (searcher *fakeLdapSearcher) SearchWithPaging(_ *ldap.SearchRequest, _ uint32) (*ldap.SearchResult, error) {
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
