package main

type reviewerCondition struct {
	ID                int             `json:"id"`
	SourceRefMatcher  branchMatcher   `json:"sourceRefMatcher"`
	TargetRefMatcher  branchMatcher   `json:"targetRefMatcher"`
	Reviewers         []bitbucketUser `json:"reviewers"`
	RequiredApprovals int             `json:"requiredApprovals"`
}

type bitbucketUser struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

type branchMatcher struct {
	ID        string      `json:"id"`
	DisplayID string      `json:"displayId"`
	Type      matcherType `json:"type"`
	Active    bool        `json:"active"`
}

type matcherType struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type reviewerChanges struct {
	Added   []bitbucketUser
	Removed []bitbucketUser
}

type bitbucketUsers struct {
	Values []bitbucketUser `json:"values"`
}

type reviewerConditionRequest struct {
	Reviewers         []bitbucketUserID `json:"reviewers"`
	SourceMatcher     branchMatcher     `json:"sourceMatcher"`
	TargetMatcher     branchMatcher     `json:"targetMatcher"`
	RequiredApprovals int               `json:"requiredApprovals"`
}

type bitbucketUserID struct {
	ID int `json:"id"`
}

type bitbucketCredentials struct {
	Username string
	Token    string
}
