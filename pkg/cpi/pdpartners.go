package cpi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Partners lists the partner IDs of the Partner Directory (/Partners; the
// tenant does not read single partners).
func (pd *PartnerDirectory) Partners() ([]string, error) {
	var rows []struct{ Pid string }
	if err := getResults(pd.exe, "/api/v1/Partners", "Get partners", &rows); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Pid)
	}
	return out, nil
}

// AlternativePartner maps an identifier of another scheme (agency, scheme,
// ID, e.g. an EDI sender ID) to a partner ID.
type AlternativePartner struct {
	Agency           string     `json:"agency"`
	Scheme           string     `json:"scheme"`
	ID               string     `json:"id"`
	Pid              string     `json:"pid"`
	CreatedBy        string     `json:"createdBy,omitempty"`
	CreatedTime      *time.Time `json:"createdTime,omitempty"`
	LastModifiedBy   string     `json:"lastModifiedBy,omitempty"`
	LastModifiedTime *time.Time `json:"lastModifiedTime,omitempty"`
}

// AlternativePartners lists the alternative partners.
func (pd *PartnerDirectory) AlternativePartners() ([]AlternativePartner, error) {
	var rows []struct {
		Agency, Scheme, Id, Pid, CreatedBy, CreatedTime, LastModifiedBy, LastModifiedTime string
	}
	if err := getResults(pd.exe, "/api/v1/AlternativePartners", "Get alternative partners", &rows); err != nil {
		return nil, err
	}
	out := make([]AlternativePartner, 0, len(rows))
	for _, r := range rows {
		out = append(out, AlternativePartner{Agency: r.Agency, Scheme: r.Scheme, ID: r.Id, Pid: r.Pid, CreatedBy: r.CreatedBy, CreatedTime: optTime(r.CreatedTime),
			LastModifiedBy: r.LastModifiedBy, LastModifiedTime: optTime(r.LastModifiedTime)})
	}
	return out, nil
}

func alternativePartnerKey(agency, scheme, id string) string {
	return fmt.Sprintf("/api/v1/AlternativePartners(Hexagency=%s,Hexscheme=%s,Hexid=%s)", keyLiteral(HexAlias(agency)), keyLiteral(HexAlias(scheme)), keyLiteral(HexAlias(id)))
}

// CreateAlternativePartner creates an alternative partner (the key fields
// are hex encoded, as for keystore aliases).
func (pd *PartnerDirectory) CreateAlternativePartner(p AlternativePartner) error {
	return pd.post("/api/v1/AlternativePartners", map[string]string{"Hexagency": HexAlias(p.Agency), "Hexscheme": HexAlias(p.Scheme), "Hexid": HexAlias(p.ID),
		"Agency": p.Agency, "Scheme": p.Scheme, "Id": p.ID, "Pid": p.Pid}, "Create alternative partner")
}

// DeleteAlternativePartner deletes an alternative partner.
func (pd *PartnerDirectory) DeleteAlternativePartner(agency, scheme, id string) error {
	return pd.delete(alternativePartnerKey(agency, scheme, id), "Delete alternative partner")
}

// AuthorizedUser allows a user (of a sender's client certificate or user
// credential) to act for a partner ID.
type AuthorizedUser struct {
	User             string     `json:"user"`
	Pid              string     `json:"pid"`
	CreatedBy        string     `json:"createdBy,omitempty"`
	CreatedTime      *time.Time `json:"createdTime,omitempty"`
	LastModifiedBy   string     `json:"lastModifiedBy,omitempty"`
	LastModifiedTime *time.Time `json:"lastModifiedTime,omitempty"`
}

// AuthorizedUsers lists the authorized users.
func (pd *PartnerDirectory) AuthorizedUsers() ([]AuthorizedUser, error) {
	var rows []struct {
		User, Pid, CreatedBy, CreatedTime, LastModifiedBy, LastModifiedTime string
	}
	if err := getResults(pd.exe, "/api/v1/AuthorizedUsers", "Get authorized users", &rows); err != nil {
		return nil, err
	}
	out := make([]AuthorizedUser, 0, len(rows))
	for _, r := range rows {
		out = append(out, AuthorizedUser{User: r.User, Pid: r.Pid, CreatedBy: r.CreatedBy, CreatedTime: optTime(r.CreatedTime),
			LastModifiedBy: r.LastModifiedBy, LastModifiedTime: optTime(r.LastModifiedTime)})
	}
	return out, nil
}

// CreateAuthorizedUser authorizes a user for a partner ID.
func (pd *PartnerDirectory) CreateAuthorizedUser(user, pid string) error {
	return pd.post("/api/v1/AuthorizedUsers", map[string]string{"User": user, "Pid": pid}, "Create authorized user")
}

// DeleteAuthorizedUser removes an authorized user.
func (pd *PartnerDirectory) DeleteAuthorizedUser(user string) error {
	return pd.delete(fmt.Sprintf("/api/v1/AuthorizedUsers(%s)", keyLiteral(user)), "Delete authorized user")
}

func (pd *PartnerDirectory) post(path string, body map[string]string, callType string) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := pd.exe.Exec(http.MethodPost, path, bytes.NewReader(payload), map[string]string{"Content-Type": "application/json", "Accept": "application/json"})
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return pdStatusError(callType, resp)
	}
	_ = resp.Body.Close()
	return nil
}

func (pd *PartnerDirectory) delete(path, callType string) error {
	resp, err := pd.exe.Exec(http.MethodDelete, path, http.NoBody, map[string]string{"Accept": "application/json"})
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusAccepted {
		return pdStatusError(callType, resp)
	}
	_ = resp.Body.Close()
	return nil
}
