package notify

import (
	"context"
	"fmt"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	nmv1alpha1 "github.com/kubesphere/notification-manager/pkg/apis/v1alpha1"
	notifyconfig "github.com/kubesphere/notification-manager/pkg/notify/config"
	"github.com/prometheus/alertmanager/config"
	"github.com/prometheus/alertmanager/notify"
	"github.com/prometheus/alertmanager/notify/email"
	"github.com/prometheus/alertmanager/template"
	"github.com/prometheus/alertmanager/types"
	"github.com/prometheus/common/model"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultSendTimeout = time.Second * 3
)

type EmailNotifier struct {
	To       []string
	Config   *config.EmailConfig
	Template *template.Template
	Timeout  time.Duration
	logger   log.Logger
}

func init() {
	Register("Email", NewEmailNotifier)
}

func NewEmailNotifier(logger log.Logger, val interface{}, opts *nmv1alpha1.Options) Notifier {

	receiver, ok := val.(*notifyconfig.Email)
	if !ok {
		_ = level.Error(logger).Log("msg", "Notifier: value type error")
		return nil
	}

	notifier := &EmailNotifier{logger: logger, To: receiver.To, Timeout: DefaultSendTimeout}
	notifier.Config = notifier.Clone(receiver.EmailConfig)
	if notifier.Config == nil {
		_ = level.Error(logger).Log("msg", "empty email config")
		return nil
	}
	if notifier.Config.Headers == nil {
		notifier.Config.Headers = make(map[string]string)
	}
	notifier.Config.HTML = `{{ template "email.default.html" . }}`

	tmpl, err := template.FromGlobs()
	if err != nil {
		_ = level.Error(notifier.logger).Log("msg", "Notifier: template error", "error", err.Error())
		return nil
	}
	notifier.Template = tmpl

	if opts != nil && opts.NotificationTimeout != nil && opts.NotificationTimeout.Email != nil {
		notifier.Timeout = time.Second * time.Duration(*opts.NotificationTimeout.Email)
	}

	return notifier
}

func (en *EmailNotifier) Notify(data []template.Data) []error {

	var errs []error
	for _, d := range data {
		en.Config.Headers["Subject"] = en.getSubject(d)
		en.Template.ExternalURL, _ = url.Parse(d.ExternalURL)

		var as []*types.Alert
		for _, a := range d.Alerts {
			as = append(as, &types.Alert{
				Alert: model.Alert{
					Labels:       kvToLabelSet(a.Labels),
					Annotations:  kvToLabelSet(a.Annotations),
					StartsAt:     a.StartsAt,
					EndsAt:       a.EndsAt,
					GeneratorURL: a.GeneratorURL,
				},
			})
		}

		sendEmail := func(to string) {
			en.Config.To = to
			e := email.New(en.Config, en.Template, en.logger)

			ctx, cancel := context.WithTimeout(context.Background(), en.Timeout)
			ctx = notify.WithGroupLabels(ctx, kvToLabelSet(d.GroupLabels))
			ctx = notify.WithReceiverName(ctx, d.Receiver)
			defer cancel()

			_, err := e.Notify(ctx, as...)
			if err != nil {
				_ = level.Error(en.logger).Log("msg", "Notifier: email notify error", "subject", en.Config.Headers["Subject"], "address", to, "error", err.Error())
				errs = append(errs, err)
			}
			_ = level.Debug(en.logger).Log("Notifier: send email to", to)
		}

		for _, to := range en.To {
			sendEmail(to)
		}
	}

	return errs
}

func (en *EmailNotifier) getSubject(data template.Data) string {

	subject := ""
	ns := data.CommonLabels["namespace"]
	alertname := data.CommonLabels["alertname"]

	firingNum := len(data.Alerts.Firing())
	if firingNum > 0 {
		subject = fmt.Sprintf("[FIRING:%d] ", firingNum)
	}

	resolvedNum := len(data.Alerts.Resolved())
	if resolvedNum > 0 {
		subject = fmt.Sprintf("%s[RESOLVED:%d] ", subject, resolvedNum)
	}

	if len(ns) > 0 {
		subject = fmt.Sprintf("%s %s", subject, ns)
	}

	if len(alertname) > 0 {
		if len(ns) > 0 {
			subject = fmt.Sprintf("%s/%s", subject, alertname)
		} else {
			subject = fmt.Sprintf("%s%s", subject, alertname)
		}
	}

	labels := ""
	for k, v := range data.CommonLabels {
		if k == "namespace" || k == "alertname" {
			continue
		}

		labels = fmt.Sprintf("%s%s=%s,", labels, k, v)
	}

	if len(labels) > 0 {
		labels = strings.TrimSuffix(labels, ",")
		subject = fmt.Sprintf("%s (%s)", subject, labels)
	}

	return subject
}

func (en *EmailNotifier) Clone(ec *config.EmailConfig) *config.EmailConfig {

	if ec == nil {
		return nil
	}

	emailConfig := &config.EmailConfig{
		NotifierConfig: config.NotifierConfig{},
		To:             "",
		From:           ec.From,
		Hello:          ec.Hello,
		Smarthost:      ec.Smarthost,
		AuthUsername:   ec.AuthUsername,
		AuthPassword:   ec.AuthPassword,
		AuthSecret:     ec.AuthSecret,
		AuthIdentity:   ec.AuthIdentity,
		Headers:        nil,
		HTML:           ec.HTML,
		Text:           ec.Text,
		RequireTLS:     &(*ec.RequireTLS),
		TLSConfig:      ec.TLSConfig,
	}

	return emailConfig
}

func kvToLabelSet(obj template.KV) model.LabelSet {

	ls := model.LabelSet{}
	for k, v := range obj {
		ls[model.LabelName(k)] = model.LabelValue(v)
	}

	return ls
}
