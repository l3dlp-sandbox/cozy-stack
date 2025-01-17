// Package contact is for managing the io.cozy.contacts documents and their
// groups.
package contact

import (
	"encoding/json"
	"strings"

	"github.com/cozy/cozy-stack/model/instance"
	"github.com/cozy/cozy-stack/pkg/consts"
	"github.com/cozy/cozy-stack/pkg/couchdb"
	"github.com/cozy/cozy-stack/pkg/couchdb/mango"
	"github.com/cozy/cozy-stack/pkg/mail"
	"github.com/cozy/cozy-stack/pkg/prefixer"
)

// Contact is a struct containing all the informations about a contact.
// We are using maps/slices/interfaces instead of structs, as it is a doctype
// that can also be used in front applications and they can add new fields. It
// would be complicated to maintain a up-to-date mapping, and failing to do so
// means that we can lose some data on JSON round-trip.
type Contact struct {
	couchdb.JSONDoc
}

// New returns a new blank contact.
func New() *Contact {
	return &Contact{
		JSONDoc: couchdb.JSONDoc{
			M: make(map[string]interface{}),
		},
	}
}

// DocType returns the contact document type
func (c *Contact) DocType() string { return consts.Contacts }

// ToMailAddress returns a struct that can be used by cozy-stack to send an
// email to this contact
func (c *Contact) ToMailAddress() (*mail.Address, error) {
	emails, ok := c.Get("email").([]interface{})
	if !ok || len(emails) == 0 {
		return nil, ErrNoMailAddress
	}
	var email string
	for i := range emails {
		obj, ok := emails[i].(map[string]interface{})
		if !ok {
			continue
		}
		address, ok := obj["address"].(string)
		if !ok {
			continue
		}
		if primary, ok := obj["primary"].(bool); ok && primary {
			email = address
		}
		if email == "" {
			email = address
		}
	}
	name := c.PrimaryName()
	return &mail.Address{Name: name, Email: email}, nil
}

// PrimaryName returns the name of the contact
func (c *Contact) PrimaryName() string {
	if fullname, ok := c.Get("fullname").(string); ok && fullname != "" {
		return fullname
	}
	name, ok := c.Get("name").(map[string]interface{})
	if !ok {
		return ""
	}
	var primary string
	if given, ok := name["givenName"].(string); ok && given != "" {
		primary = given
	}
	if family, ok := name["familyName"].(string); ok && family != "" {
		if primary != "" {
			primary += " "
		}
		primary += family
	}
	return primary
}

// SortingKey returns a string that can be used for sorting the contacts like
// in the contacts app.
func (c *Contact) SortingKey() string {
	indexes, ok := c.Get("indexes").(map[string]interface{})
	if !ok {
		return c.PrimaryName()
	}
	str, ok := indexes["byFamilyNameGivenNameEmailCozyUrl"].(string)
	if !ok {
		return c.PrimaryName()
	}
	return str
}

// PrimaryPhoneNumber returns the preferred phone number,
// or a blank string if the contact has no known phone number.
func (c *Contact) PrimaryPhoneNumber() string {
	phones, ok := c.Get("phone").([]interface{})
	if !ok || len(phones) == 0 {
		return ""
	}
	var number string
	for i := range phones {
		phone, ok := phones[i].(map[string]interface{})
		if !ok {
			continue
		}
		n, ok := phone["number"].(string)
		if !ok {
			continue
		}
		if primary, ok := phone["primary"].(bool); ok && primary {
			number = n
		}
		if number == "" {
			number = n
		}
	}
	return number
}

// PrimaryCozyURL returns the URL of the primary cozy,
// or a blank string if the contact has no known cozy.
func (c *Contact) PrimaryCozyURL() string {
	cozys, ok := c.Get("cozy").([]interface{})
	if !ok || len(cozys) == 0 {
		return ""
	}
	var url string
	for i := range cozys {
		cozy, ok := cozys[i].(map[string]interface{})
		if !ok {
			continue
		}
		u, ok := cozy["url"].(string)
		if !ok {
			continue
		}
		if primary, ok := cozy["primary"].(bool); ok && primary {
			url = u
		}
		if url == "" {
			url = u
		}
	}
	if url != "" && !strings.HasPrefix(url, "http") {
		url = "https://" + url
	}
	return url
}

// GroupIDs returns the list of the group identifiers that this contact belongs to.
func (c *Contact) GroupIDs() []string {
	rels, ok := c.Get("relationships").(map[string]interface{})
	if !ok {
		return nil
	}

	var groupIDs []string

	for _, groups := range rels {
		if groups, ok := groups.(map[string]interface{}); ok {
			if data, ok := groups["data"].([]interface{}); ok {
				for _, item := range data {
					if item, ok := item.(map[string]interface{}); ok {
						if item["_type"] == consts.Groups {
							if id, ok := item["_id"].(string); ok {
								groupIDs = append(groupIDs, id)
							}
						}
					}
				}
			}
		}
	}

	return groupIDs
}

// AddNameIfMissing can be used to add a name if there was none. We need the
// email address to ignore it if the displayName was updated with it by a
// service of the contacts application.
func (c *Contact) AddNameIfMissing(db prefixer.Prefixer, name, email string) error {
	was, ok := c.Get("displayName").(string)
	if ok && len(was) > 0 && was != email {
		return nil
	}
	was, ok = c.Get("fullname").(string)
	if ok && len(was) > 0 {
		return nil
	}
	c.M["displayName"] = name
	c.M["fullname"] = name
	return couchdb.UpdateDoc(db, c)
}

// AddCozyURL adds a cozy URL to this contact (unless the contact has already
// this cozy URL) and saves the contact.
func (c *Contact) AddCozyURL(db prefixer.Prefixer, cozyURL string) error {
	cozys, ok := c.Get("cozy").([]interface{})
	if !ok {
		cozys = []interface{}{}
	}
	for i := range cozys {
		cozy, ok := cozys[i].(map[string]interface{})
		if !ok {
			continue
		}
		u, ok := cozy["url"].(string)
		if ok && cozyURL == u {
			return nil
		}
	}
	cozy := map[string]interface{}{"url": cozyURL}
	c.M["cozy"] = append([]interface{}{cozy}, cozys...)
	return couchdb.UpdateDoc(db, c)
}

// ChangeCozyURL is used when a contact has moved their Cozy to a new URL.
func (c *Contact) ChangeCozyURL(db prefixer.Prefixer, cozyURL string) error {
	cozy := map[string]interface{}{"url": cozyURL}
	c.M["cozy"] = []interface{}{cozy}
	return couchdb.UpdateDoc(db, c)
}

// Find returns the contact stored in database from a given ID
func Find(db prefixer.Prefixer, contactID string) (*Contact, error) {
	doc := &Contact{}
	err := couchdb.GetDoc(db, consts.Contacts, contactID, doc)
	return doc, err
}

// FindByEmail returns the contact with the given email address, when possible
func FindByEmail(db prefixer.Prefixer, email string) (*Contact, error) {
	var res couchdb.ViewResponse
	err := couchdb.ExecView(db, couchdb.ContactByEmail, &couchdb.ViewRequest{
		Key:         email,
		IncludeDocs: true,
		Limit:       1,
	}, &res)
	if err != nil {
		return nil, err
	}
	if len(res.Rows) == 0 {
		return nil, ErrNotFound
	}
	doc := &Contact{}
	err = json.Unmarshal(res.Rows[0].Doc, &doc)
	return doc, err
}

// CreateMyself creates the myself contact document from the instance settings.
func CreateMyself(inst *instance.Instance, settings *couchdb.JSONDoc) (*Contact, error) {
	doc := New()
	doc.JSONDoc.M["me"] = true
	email, ok := settings.M["email"].(string)
	if ok {
		doc.JSONDoc.M["email"] = []map[string]interface{}{
			{"address": email, "primary": true},
		}
	}
	name, _ := settings.M["public_name"].(string)
	displayName := name
	if name == "" {
		parts := strings.SplitN(email, "@", 2)
		name = parts[0]
		displayName = email
	}
	if name != "" {
		doc.JSONDoc.M["fullname"] = name
		doc.JSONDoc.M["displayName"] = displayName
	}
	cozyURL := inst.PageURL("", nil)
	doc.JSONDoc.M["cozy"] = []map[string]interface{}{
		{"url": cozyURL, "primary": true},
	}
	index := email
	if index == "" {
		index = cozyURL
	}
	doc.JSONDoc.M["indexes"] = map[string]interface{}{
		"byFamilyNameGivenNameEmailCozyUrl": index,
	}
	if err := couchdb.CreateDoc(inst, doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// GetMyself returns the myself contact document, or an ErrNotFound error.
func GetMyself(db prefixer.Prefixer) (*Contact, error) {
	var docs []*Contact
	req := &couchdb.FindRequest{
		UseIndex: "by-me",
		Selector: mango.Equal("me", true),
		Limit:    1,
	}
	err := couchdb.FindDocs(db, consts.Contacts, req, &docs)
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, ErrNotFound
	}
	return docs[0], nil
}

var _ couchdb.Doc = &Contact{}
