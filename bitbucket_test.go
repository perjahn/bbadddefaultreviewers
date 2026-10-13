package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestParseRepositoryUrl(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		project   string
		repo      string
		baseUrl   string
		wantError bool
	}{
		{
			name:    "repository browse Url",
			input:   "https://bitbucket.example.com/projects/ENG/repos/service/browse",
			project: "ENG",
			repo:    "service",
			baseUrl: "https://bitbucket.example.com",
		},
		{
			name:    "clone Url with context path",
			input:   "https://bitbucket.example.com/bitbucket/scm/ENG/service.git",
			project: "ENG",
			repo:    "service",
			baseUrl: "https://bitbucket.example.com/bitbucket",
		},
		{name: "missing repository route", input: "https://bitbucket.example.com/", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			project, repo, baseUrl, err := parseRepositoryUrl(test.input)
			if (err != nil) != test.wantError {
				t.Fatalf("parseRepositoryUrl() error = %v, wantError %v", err, test.wantError)
			}
			if err == nil && (project != test.project || repo != test.repo || baseUrl != test.baseUrl) {
				t.Fatalf("parseRepositoryUrl() = (%q, %q, %q), want (%q, %q, %q)", project, repo, baseUrl, test.project, test.repo, test.baseUrl)
			}
		})
	}
}

func TestParseRepositoryUrls(t *testing.T) {
	baseUrl, repositories, err := parseRepositoryUrls([]string{
		"https://bitbucket.example.com/bitbucket/projects/ENG/repos/service",
		"https://bitbucket.example.com/bitbucket/scm/OPS/deployments.git",
	})
	if err != nil {
		t.Fatalf("parseRepositoryUrls() error = %v", err)
	}
	if baseUrl != "https://bitbucket.example.com/bitbucket" {
		t.Errorf("base url = %q, want %q", baseUrl, "https://bitbucket.example.com/bitbucket")
	}
	wantRepositories := []repositoryTarget{
		{projectKey: "ENG", repoSlug: "service"},
		{projectKey: "OPS", repoSlug: "deployments"},
	}
	if !reflect.DeepEqual(repositories, wantRepositories) {
		t.Errorf("repositories = %#v, want %#v", repositories, wantRepositories)
	}
}

func TestParseRepositoryUrlsRequiresSameServer(t *testing.T) {
	_, _, err := parseRepositoryUrls([]string{
		"https://one.example.com/projects/ENG/repos/service",
		"https://two.example.com/projects/OPS/repos/deployments",
	})
	if err == nil {
		t.Fatal("parseRepositoryUrls() error = nil, want different-server error")
	}
}

func TestRepoUrlFlagRepeatsAndOverridesEnvironment(t *testing.T) {
	flagValue := repoUrlFlag{values: []string{"https://bitbucket.example.com/projects/ENG/repos/old"}}
	if err := flagValue.Set("https://bitbucket.example.com/projects/ENG/repos/one"); err != nil {
		t.Fatalf("first Set() error = %v", err)
	}
	if err := flagValue.Set("https://bitbucket.example.com/projects/OPS/repos/two"); err != nil {
		t.Fatalf("second Set() error = %v", err)
	}
	want := []string{
		"https://bitbucket.example.com/projects/ENG/repos/one",
		"https://bitbucket.example.com/projects/OPS/repos/two",
	}
	if !reflect.DeepEqual(flagValue.values, want) {
		t.Errorf("repo url values = %#v, want %#v", flagValue.values, want)
	}
	if got := splitValues("https://bitbucket.example.com/projects/ENG/repos/one;\thttps://bitbucket.example.com/projects/OPS/repos/two"); !reflect.DeepEqual(got, []string{want[0], want[1]}) {
		t.Errorf("splitValues() for repository URLs = %#v, want %#v", got, want)
	}

	gotGroupsAndUsers := splitValues("Group1,Group2; User1\nUser2")
	wantGroupsAndUsers := []string{"Group1", "Group2", "User1", "User2"}
	if !reflect.DeepEqual(gotGroupsAndUsers, wantGroupsAndUsers) {
		t.Errorf("splitValues() for groups and users = %#v, want %#v", gotGroupsAndUsers, wantGroupsAndUsers)
	}
}

func TestConfigureRepositoryTargetsContinuesAfterFailure(t *testing.T) {
	repositories := []repositoryTarget{
		{projectKey: "ENG", repoSlug: "service"},
		{projectKey: "OPS", repoSlug: "deployments"},
	}
	var configured []repositoryTarget
	err := configureRepositoryTargets(repositories, func(repository repositoryTarget) error {
		configured = append(configured, repository)
		if repository.projectKey == "ENG" {
			return errors.New("request failed")
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "ENG/service") {
		t.Fatalf("configureRepositoryTargets() error = %v, want first repository failure", err)
	}
	if !reflect.DeepEqual(configured, repositories) {
		t.Errorf("configured repositories = %#v, want %#v", configured, repositories)
	}
}

func TestBranchMatcher(t *testing.T) {
	sourceMatcher := branchMatcher{
		ID:        "refs/heads/mybranch",
		DisplayID: "mybranch",
		Type:      matcherType{ID: "BRANCH", Name: "Branch"},
		Active:    true,
	}
	targetMatcher := branchMatcher{
		ID:        "refs/heads/master",
		DisplayID: "master",
		Type:      matcherType{ID: "BRANCH", Name: "Branch"},
		Active:    true,
	}

	if !isSameCondition(sourceMatcher, "mybranch", targetMatcher, "master") {
		t.Fatal("matching branches should match")
	}

	if isSameCondition(sourceMatcher, "mybranch2", targetMatcher, "master") {
		t.Fatal("non-matching source branch should not match")
	}

	if isSameCondition(sourceMatcher, "mybranch", targetMatcher, "master2") {
		t.Fatal("non-matching target branch should not match")
	}
}

func TestBranchMatcherAnySource(t *testing.T) {
	sourceMatcher := branchMatcher{
		ID:        "ANY_REF_MATCHED_ID",
		DisplayID: "ANY_REF_MATCHED_ID",
		Type:      matcherType{ID: "ANY_REF", Name: "Any branch"},
		Active:    true,
	}
	targetMatcher := branchMatcher{
		ID:        "refs/heads/master",
		DisplayID: "master",
		Type:      matcherType{ID: "BRANCH", Name: "Branch"},
		Active:    true,
	}

	if !isSameCondition(sourceMatcher, "", targetMatcher, "master") {
		t.Fatal("matching any-branch should match")
	}

	if isSameCondition(sourceMatcher, "mybranch", targetMatcher, "master") {
		t.Fatal("non-matching any-branch should not match")
	}
}

func TestBranchMatcherAnyTarget(t *testing.T) {
	sourceMatcher := branchMatcher{
		ID:        "refs/heads/mybranch",
		DisplayID: "mybranch",
		Type:      matcherType{ID: "BRANCH", Name: "Branch"},
		Active:    true,
	}
	targetMatcher := branchMatcher{
		ID:        "ANY_REF_MATCHED_ID",
		DisplayID: "ANY_REF_MATCHED_ID",
		Type:      matcherType{ID: "ANY_REF", Name: "Any branch"},
		Active:    true,
	}

	if !isSameCondition(sourceMatcher, "mybranch", targetMatcher, "") {
		t.Fatal("matching any target branch should match")
	}

	if isSameCondition(sourceMatcher, "mybranch", targetMatcher, "master") {
		t.Fatal("non-matching any target branch should not match")
	}
}

func TestRunLoopStopsWhenRunnerReturnsStopSignal(t *testing.T) {
	calls := 0
	err := runLoop(1, func() error {
		calls++
		if calls == 2 {
			return errStopLoop
		}
		return nil
	})
	if err != nil {
		t.Fatalf("runLoop() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("runLoop() calls = %d, want 2", calls)
	}
}

func TestConfigureDefaultReviewersReturnsHttpErrorWithoutRetrying(t *testing.T) {
	t.Chdir(t.TempDir())
	var methods []string
	var posted [][]int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		username, token, ok := request.BasicAuth()
		if !ok || username != "automation" || token != "secret" {
			t.Errorf("unexpected basic auth credentials")
		}
		if request.Method == http.MethodGet {
			switch request.URL.Path {
			case "/bitbucket/rest/default-reviewers/1.0/projects/ENG/repos/service/conditions":
				writer.Write([]byte(`[]`))
			case "/bitbucket/rest/api/latest/admin/users":
				if request.URL.Query().Get("filter") == "valid-user" {
					writer.Write([]byte(`{"values":[{"id":1,"name":"valid-user","displayName":"Valid User"}]}`))
				} else {
					writer.Write([]byte(`{"values":[{"id":2,"name":"invalid-user","displayName":"Invalid User"}]}`))
				}
			default:
				t.Errorf("conditions path = %s", request.URL.Path)
				http.NotFound(writer, request)
			}
			return
		}
		methods = append(methods, request.Method)
		if request.URL.Path != "/bitbucket/rest/default-reviewers/1.0/projects/ENG/repos/service/condition" {
			t.Errorf("write path = %s", request.URL.Path)
		}
		var payload reviewerConditionRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode request payload: %v", err)
		}
		accounts := make([]int, len(payload.Reviewers))
		for index, reviewer := range payload.Reviewers {
			accounts[index] = reviewer.ID
		}
		posted = append(posted, accounts)
		if payload.SourceMatcher.ID != "refs/heads/feature" || payload.TargetMatcher.ID != "refs/heads/main" || payload.RequiredApprovals != 2 {
			t.Errorf("unexpected branch matchers or required approvals: %#v", payload)
		}
		if len(posted) == 1 {
			writer.WriteHeader(http.StatusBadRequest)
			io.WriteString(writer, `{"errors":[{"message":"User invalid-user was not found"}]}`)
			return
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	err := configureDefaultReviewers(server.URL+"/bitbucket", "ENG", "service", "feature", "main", 2, []string{"valid-user", "invalid-user"}, bitbucketCredentials{Username: "automation", Token: "secret"}, false, io.Discard)
	if err == nil {
		t.Fatal("configureDefaultReviewers() error = nil, want HTTP 400 error")
	}

	wantMethods := []string{http.MethodPost}
	if !reflect.DeepEqual(methods, wantMethods) {
		t.Errorf("methods = %#v, want %#v", methods, wantMethods)
	}
	want := [][]int{{1, 2}}
	if !reflect.DeepEqual(posted, want) {
		t.Errorf("posted user IDs = %#v, want %#v", posted, want)
	}
}

func TestConfigureDefaultReviewersCreatesAnyRefMatchers(t *testing.T) {
	t.Chdir(t.TempDir())
	var payload reviewerConditionRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			switch request.URL.Path {
			case "/rest/api/latest/admin/users":
				writer.Write([]byte(`{"values":[{"id":1,"name":"reviewer","displayName":"Reviewer"}]}`))
			case "/rest/default-reviewers/1.0/projects/ENG/repos/service/conditions":
				writer.Write([]byte(`[]`))
			default:
				t.Errorf("unexpected GET request to %s", request.URL.Path)
				http.NotFound(writer, request)
			}
			return
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode request payload: %v", err)
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	err := configureDefaultReviewers(server.URL, "ENG", "service", "", "", 1, []string{"reviewer"}, bitbucketCredentials{Username: "automation", Token: "secret"}, false, io.Discard)
	if err != nil {
		t.Fatalf("configureDefaultReviewers() error = %v", err)
	}
	want := branchMatcher{
		ID:        "ANY_REF_MATCHER_ID",
		DisplayID: "ANY_REF_MATCHER_ID",
		Type:      matcherType{ID: "ANY_REF", Name: "Any branch"},
		Active:    true,
	}
	if !reflect.DeepEqual(payload.SourceMatcher, want) || !reflect.DeepEqual(payload.TargetMatcher, want) {
		t.Errorf("any-ref matchers = (%#v, %#v), want (%#v, %#v)", payload.SourceMatcher, payload.TargetMatcher, want, want)
	}
}

func TestConfigureDefaultReviewersUpdatesMatchingCondition(t *testing.T) {
	t.Chdir(t.TempDir())
	var method string
	var requestPath string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			switch request.URL.Path {
			case "/rest/api/latest/admin/users":
				writer.Write([]byte(`{"values":[{"id":1,"name":"reviewer","displayName":"Reviewer"}]}`))
			case "/rest/default-reviewers/1.0/projects/ENG/repos/service/conditions":
				writer.Write([]byte(`[{"id":42,"sourceRefMatcher":{"id":"refs/heads/feature","displayId":"feature","type":{"id":"BRANCH","name":"Branch"}},"targetRefMatcher":{"id":"refs/heads/main","displayId":"main","type":{"id":"BRANCH","name":"Branch"}}}]`))
			default:
				t.Errorf("unexpected GET request to %s", request.URL.Path)
				http.NotFound(writer, request)
			}
			return
		}
		method = request.Method
		requestPath = request.URL.Path
		var payload reviewerConditionRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode update payload: %v", err)
		}
		if payload.RequiredApprovals != 3 || len(payload.Reviewers) != 1 || payload.Reviewers[0].ID != 1 {
			t.Errorf("unexpected update payload: %#v", payload)
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	err := configureDefaultReviewers(server.URL, "ENG", "service", "feature", "main", 3, []string{"reviewer"}, bitbucketCredentials{Username: "automation", Token: "secret"}, false, io.Discard)
	if err != nil {
		t.Fatalf("ConfigureDefaultReviewersUpdatesMatchingCondition() error = %v", err)
	}
	if method != http.MethodPut || requestPath != "/rest/default-reviewers/1.0/projects/ENG/repos/service/condition/42" {
		t.Errorf("update request = %s %s, want PUT /rest/default-reviewers/1.0/projects/ENG/repos/service/condition/42", method, requestPath)
	}
}

func TestConfigureDefaultReviewersDryRunPrintsDiffWithoutWriting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("dry-run made unexpected %s request to %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		switch request.URL.Path {
		case "/rest/api/latest/admin/users":
			if request.URL.Query().Get("filter") == "keep" {
				writer.Write([]byte(`{"values":[{"id":1,"name":"keep","displayName":"Keep"}]}`))
			} else {
				writer.Write([]byte(`{"values":[{"id":3,"name":"add","displayName":"Add"}]}`))
			}
		case "/rest/default-reviewers/1.0/projects/ENG/repos/service/conditions":
			writer.Write([]byte(`[{"id":42,"sourceRefMatcher":{"id":"refs/heads/feature","displayId":"feature","type":{"id":"BRANCH","name":"Branch"}},"targetRefMatcher":{"id":"refs/heads/main","displayId":"main","type":{"id":"BRANCH","name":"Branch"}},"reviewers":[{"id":1,"name":"keep"},{"id":2,"name":"remove","displayName":"Remove"}],"requiredApprovals":2}]`))
		default:
			t.Errorf("unexpected GET request to %s", request.URL.Path)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	var output bytes.Buffer
	err := configureDefaultReviewers(server.URL, "ENG", "service", "feature", "main", 3, []string{"keep", "add"}, bitbucketCredentials{Username: "automation", Token: "secret"}, true, &output)
	if err != nil {
		t.Fatalf("configureDefaultReviewers() error = %v", err)
	}

	want := "Reviewers to add (1):\n  + add (Add)\nReviewers to remove (1):\n  - remove (Remove)\nFound existing branch condition:\n  refs/heads/feature (feature) (BRANCH) -> refs/heads/main (main) (BRANCH)\n  refs/heads/feature (feature) (BRANCH) -> refs/heads/main (main) (BRANCH)\nRequired approvals: 2 -> 3\n"
	if output.String() != want {
		t.Errorf("dry-run diff = %q, want %q", output.String(), want)
	}
}

func TestConfigureDefaultReviewersDryRunShowsNewAnyBranchCondition(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("dry-run made unexpected %s request to %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		switch request.URL.Path {
		case "/rest/api/latest/admin/users":
			writer.Write([]byte(`{"values":[{"id":1,"name":"reviewer","displayName":"Reviewer"}]}`))
		case "/rest/default-reviewers/1.0/projects/ENG/repos/service/conditions":
			writer.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected GET request to %s", request.URL.Path)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	var output bytes.Buffer
	err := configureDefaultReviewers(server.URL, "ENG", "service", "", "main", 1, []string{"reviewer"}, bitbucketCredentials{Username: "automation", Token: "secret"}, true, &output)
	if err != nil {
		t.Fatalf("configureDefaultReviewers() error = %v", err)
	}
	want := "Reviewers to add (1):\n  + reviewer (Reviewer)\nReviewers to remove (0):\n  (none)\nDidn't find existing branch condition:\n  ANY_REF_MATCHER_ID (ANY_REF_MATCHER_ID) (ANY_REF) -> refs/heads/main (main) (BRANCH)\nRequired approvals: (new condition) -> 1\n"
	if output.String() != want {
		t.Errorf("dry-run diff = %q, want %q", output.String(), want)
	}
}
