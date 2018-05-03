package move

import (
	"encoding/base64"
	"fmt"
	"runtime"
	"time"

	"github.com/cozy/cozy-stack/pkg/consts"
	"github.com/cozy/cozy-stack/pkg/instance"
	"github.com/cozy/cozy-stack/pkg/jobs"
	"github.com/cozy/cozy-stack/pkg/workers/mails"
)

func init() {
	jobs.AddWorker(&jobs.WorkerConfig{
		WorkerType:   "export",
		Concurrency:  runtime.NumCPU(),
		MaxExecCount: 1,
		Timeout:      10 * 60 * time.Second,
		WorkerFunc:   ExportWorker,
	})
}

// ExportOptions contains the options for launching the export worker.
type ExportOptions struct {
	BucketSize   int64         `json:"bucket_size"`
	MaxAge       time.Duration `json:"max_age"`
	WithDoctypes []string      `json:"with_doctypes,omitempty"`
	WithoutIndex bool          `json:"without_index,omitempty"`
}

// ExportWorker is the worker responsible for creating an export of the
// instance.
func ExportWorker(c *jobs.WorkerContext) error {
	var opts ExportOptions
	if err := c.UnmarshalMessage(&opts); err != nil {
		return err
	}

	i, err := instance.Get(c.Domain())
	if err != nil {
		return err
	}

	exportDoc, err := Export(i, opts, SystemArchiver())
	if err != nil {
		return err
	}

	mac := base64.URLEncoding.EncodeToString(exportDoc.GenerateAuthMessage(i))
	link := i.SubDomain(consts.SettingsSlug)
	link.Fragment = fmt.Sprintf("/exports/%s", mac)
	mail := mails.Options{
		Mode:           mails.ModeNoReply,
		TemplateName:   "archiver",
		TemplateValues: map[string]string{"ArchiveLink": link.String()},
	}

	msg, err := jobs.NewMessage(&mail)
	if err != nil {
		return err
	}

	_, err = jobs.System().PushJob(&jobs.JobRequest{
		Domain:     i.Domain,
		WorkerType: "sendmail",
		Message:    msg,
	})
	return err
}
