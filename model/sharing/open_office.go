package sharing

import (
	"bytes"
	"net/url"

	"github.com/cozy/cozy-stack/client/request"
	"github.com/cozy/cozy-stack/model/instance"
	"github.com/cozy/cozy-stack/model/office"
	"github.com/cozy/cozy-stack/model/settings"
	"github.com/cozy/cozy-stack/model/vfs"
	"github.com/cozy/cozy-stack/pkg/config/config"
	"github.com/cozy/cozy-stack/pkg/consts"
	"github.com/cozy/cozy-stack/pkg/couchdb"
	"github.com/cozy/cozy-stack/pkg/jsonapi"
	jwt "github.com/golang-jwt/jwt/v5"
)

type apiOfficeURL struct {
	FileID     string      `json:"_id,omitempty"`
	DocID      string      `json:"document_id"`
	Subdomain  string      `json:"subdomain"`
	Protocol   string      `json:"protocol"`
	Instance   string      `json:"instance"`
	Sharecode  string      `json:"sharecode,omitempty"`
	PublicName string      `json:"public_name,omitempty"`
	OO         *onlyOffice `json:"onlyoffice,omitempty"`
}

type onlyOffice struct {
	URL   string `json:"url,omitempty"`
	Token string `json:"token,omitempty"`
	Type  string `json:"documentType"`
	Doc   struct {
		Filetype string `json:"filetype,omitempty"`
		Key      string `json:"key"`
		Title    string `json:"title,omitempty"`
		URL      string `json:"url"`
		Info     struct {
			Owner    string `json:"owner,omitempty"`
			Uploaded string `json:"uploaded,omitempty"`
		} `json:"info"`
	} `json:"document"`
	Editor struct {
		Callback string `json:"callbackUrl"`
		Lang     string `json:"lang,omitempty"`
		Mode     string `json:"mode"`
		Custom   struct {
			CompactHeader bool `json:"compactHeader"`
			Customer      struct {
				Address string `json:"address"`
				Logo    string `json:"logo"`
				Mail    string `json:"mail"`
				Name    string `json:"name"`
				WWW     string `json:"www"`
			} `json:"customer"`
			Feedback  bool `json:"feedback"`
			ForceSave bool `json:"forcesave"`
			GoBack    bool `json:"goback"`
		} `json:"customization"`
	} `json:"editor"`
}

func (o *apiOfficeURL) ID() string                             { return o.FileID }
func (o *apiOfficeURL) Rev() string                            { return "" }
func (o *apiOfficeURL) DocType() string                        { return consts.OfficeURL }
func (o *apiOfficeURL) Clone() couchdb.Doc                     { cloned := *o; return &cloned }
func (o *apiOfficeURL) SetID(id string)                        { o.FileID = id }
func (o *apiOfficeURL) SetRev(rev string)                      {}
func (o *apiOfficeURL) Relationships() jsonapi.RelationshipMap { return nil }
func (o *apiOfficeURL) Included() []jsonapi.Object             { return nil }
func (o *apiOfficeURL) Links() *jsonapi.LinksList              { return nil }
func (o *apiOfficeURL) Fetch(field string) []string            { return nil }

func (o *apiOfficeURL) sign(cfg *config.Office) (string, error) {
	if cfg == nil || cfg.InboxSecret == "" {
		return "", nil
	}

	claims := *o.OO
	claims.URL = ""
	claims.Doc.Filetype = ""
	claims.Doc.Title = ""
	claims.Doc.Info.Owner = ""
	claims.Doc.Info.Uploaded = ""
	claims.Editor.Lang = ""
	claims.Editor.Custom.Customer.Address = ""
	claims.Editor.Custom.Customer.Logo = ""
	claims.Editor.Custom.Customer.Mail = ""
	claims.Editor.Custom.Customer.Name = ""
	claims.Editor.Custom.Customer.WWW = ""
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, &claims)
	return token.SignedString([]byte(cfg.InboxSecret))
}

func (o *onlyOffice) GetExpirationTime() (*jwt.NumericDate, error) { return nil, nil }
func (o *onlyOffice) GetIssuedAt() (*jwt.NumericDate, error)       { return nil, nil }
func (o *onlyOffice) GetNotBefore() (*jwt.NumericDate, error)      { return nil, nil }
func (o *onlyOffice) GetIssuer() (string, error)                   { return "", nil }
func (o *onlyOffice) GetSubject() (string, error)                  { return "", nil }
func (o *onlyOffice) GetAudience() (jwt.ClaimStrings, error)       { return nil, nil }

// OfficeOpener can be used to find the parameters for opening an office document.
type OfficeOpener struct {
	*FileOpener
}

// Open will return an OfficeOpener for the given file.
func OpenOffice(inst *instance.Instance, fileID string) (*OfficeOpener, error) {
	file, err := inst.VFS().FileByID(fileID)
	if err != nil {
		return nil, err
	}

	// Check that the file is an office document
	if !isOfficeDocument(file) {
		return nil, office.ErrInvalidFile
	}

	opener, err := NewFileOpener(inst, file)
	if err != nil {
		return nil, err
	}
	return &OfficeOpener{opener}, nil
}

// GetResult looks if the file can be opened locally or not, which code can be
// used in case of a shared office document, and other parameters.. and returns
// the information.
func (o *OfficeOpener) GetResult(memberIndex int, readOnly bool) (jsonapi.Object, error) {
	var result *apiOfficeURL
	var err error
	if o.ShouldOpenLocally() {
		result, err = o.openLocalDocument(memberIndex, readOnly)
	} else {
		result, err = o.openSharedDocument()
	}
	if err != nil {
		return nil, err
	}

	result.FileID = o.File.ID()
	return result, nil
}

func (o *OfficeOpener) openLocalDocument(memberIndex int, readOnly bool) (*apiOfficeURL, error) {
	cfg := office.GetConfig(o.Inst.ContextName)
	if cfg == nil || cfg.OnlyOfficeURL == "" {
		return nil, office.ErrNoServer
	}

	// Create a local result
	code, err := o.GetSharecode(memberIndex, readOnly)
	if err != nil {
		return nil, err
	}
	params := o.OpenLocalFile(code)
	doc := apiOfficeURL{
		DocID:     params.FileID,
		Protocol:  params.Protocol,
		Subdomain: params.Subdomain,
		Instance:  params.Instance,
		Sharecode: params.Sharecode,
	}

	// Fill the parameters for the Document Server
	mode := "edit"
	if readOnly || o.File.Trashed {
		mode = "view"
	}
	download, err := o.downloadURL()
	if err != nil {
		o.Inst.Logger().WithNamespace("office").
			Infof("Cannot build download URL: %s", err)
		return nil, ErrInternalServerError
	}
	key, err := office.GetStore().GetSecretByID(o.Inst, o.File.ID())
	if err != nil {
		o.Inst.Logger().WithNamespace("office").
			Infof("Cannot get secret from store: %s", err)
		return nil, ErrInternalServerError
	}
	if key != "" {
		doc, err := office.GetStore().GetDoc(o.Inst, key)
		if err != nil {
			o.Inst.Logger().WithNamespace("office").
				Infof("Cannot get doc from store: %s", err)
			return nil, ErrInternalServerError
		}
		if shouldOpenANewVersion(o.File, doc) {
			key = ""
		}
	}
	if key == "" {
		detector := office.ConflictDetector{ID: o.File.ID(), Rev: o.File.Rev(), MD5Sum: o.File.MD5Sum}
		key, err = office.GetStore().AddDoc(o.Inst, detector)
	}
	if err != nil {
		o.Inst.Logger().WithNamespace("office").
			Infof("Cannot add doc to store: %s", err)
		return nil, ErrInternalServerError
	}
	publicName, _ := settings.PublicName(o.Inst)
	doc.PublicName = publicName
	doc.OO = &onlyOffice{
		URL:  cfg.OnlyOfficeURL,
		Type: documentType(o.File),
	}
	doc.OO.Doc.Filetype = o.File.Mime
	doc.OO.Doc.Key = key
	doc.OO.Doc.Title = o.File.DocName
	doc.OO.Doc.URL = download
	doc.OO.Doc.Info.Owner = publicName
	doc.OO.Doc.Info.Uploaded = uploadedDate(o.File)
	doc.OO.Editor.Callback = o.Inst.PageURL("/office/callback", nil)
	doc.OO.Editor.Lang = o.Inst.Locale
	doc.OO.Editor.Mode = mode
	doc.OO.Editor.Custom.CompactHeader = true
	doc.OO.Editor.Custom.Customer.Address = "\"Le Surena\" Face au 5 Quai Marcel Dassault 92150 Suresnes"
	doc.OO.Editor.Custom.Customer.Logo = o.Inst.FromURL(&url.URL{Path: "/assets/icon-192.png"})
	doc.OO.Editor.Custom.Customer.Mail = o.Inst.SupportEmailAddress()
	doc.OO.Editor.Custom.Customer.Name = "Twake Workplace"
	doc.OO.Editor.Custom.Customer.WWW = "cozy.io"
	doc.OO.Editor.Custom.Feedback = false
	doc.OO.Editor.Custom.ForceSave = true
	doc.OO.Editor.Custom.GoBack = false

	token, err := doc.sign(cfg)
	if err != nil {
		return nil, err
	}
	doc.OO.Token = token
	return &doc, nil
}

func (o *OfficeOpener) openSharedDocument() (*apiOfficeURL, error) {
	prepared, err := o.PrepareRequestForSharedFile()
	if err != nil {
		return nil, err
	}
	if prepared.Opts == nil {
		return o.openLocalDocument(prepared.MemberIndex, prepared.ReadOnly)
	}

	prepared.Opts.Path = "/office/" + prepared.XoredID + "/open"
	res, err := request.Req(prepared.Opts)
	if res != nil && res.StatusCode/100 == 4 {
		res, err = RefreshToken(o.Inst, err, o.Sharing, prepared.Creator,
			prepared.Creds, prepared.Opts, nil)
	}
	if res != nil && res.StatusCode == 404 {
		return o.openLocalDocument(prepared.MemberIndex, prepared.ReadOnly)
	}
	if err != nil {
		o.Inst.Logger().WithNamespace("office").Infof("openSharedDocument error: %s", err)
		return nil, ErrInternalServerError
	}
	defer res.Body.Close()
	var doc apiOfficeURL
	if _, err := jsonapi.Bind(res.Body, &doc); err != nil {
		return nil, err
	}
	publicName, _ := settings.PublicName(o.Inst)
	doc.PublicName = publicName
	doc.OO = nil
	return &doc, nil
}

// downloadURL returns an URL where the Document Server can download the file.
func (o *OfficeOpener) downloadURL() (string, error) {
	path, err := o.File.Path(o.Inst.VFS())
	if err != nil {
		return "", err
	}
	secret, err := vfs.GetStore().AddFile(o.Inst, path)
	if err != nil {
		return "", err
	}
	return o.Inst.PageURL("/files/downloads/"+secret+"/"+o.File.DocName, nil), nil
}

// uploadedDate returns the uploaded date for a file in the date format used by
// OnlyOffice
func uploadedDate(f *vfs.FileDoc) string {
	date := f.CreatedAt
	if f.CozyMetadata != nil && f.CozyMetadata.UploadedAt != nil {
		date = *f.CozyMetadata.UploadedAt
	}
	return date.Format("2006-01-02 3:04 PM")
}

// documentType returns the document type parameter for Only Office
// Cf https://api.onlyoffice.com/editors/config/#documentType
func documentType(f *vfs.FileDoc) string {
	switch f.Class {
	case "spreadsheet":
		return "cell"
	case "slide":
		return "slide"
	default:
		return "word"
	}
}

func isOfficeDocument(f *vfs.FileDoc) bool {
	switch f.Class {
	case "spreadsheet", "slide", "text":
		return true
	default:
		return false
	}
}

func shouldOpenANewVersion(file *vfs.FileDoc, detector *office.ConflictDetector) bool {
	if detector == nil {
		return true
	}
	cm := file.CozyMetadata
	if cm != nil && cm.UploadedBy != nil && cm.UploadedBy.Slug == office.OOSlug {
		return false
	}
	if bytes.Equal(file.MD5Sum, detector.MD5Sum) {
		return false
	}
	return true
}
