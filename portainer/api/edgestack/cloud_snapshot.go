package edgestack

// A summary owns one immutable key/model snapshot for both redaction and the
// outgoing request. Rotation affects only summaries created afterwards.
func (s *Service) freezeSummaryProvider() (Provider, []string, error) {
	if s.cloud != nil {
		record, err := s.cloud.record()
		if err != nil {
			return nil, nil, providerError("provider_not_configured")
		}
		_, deepSeek := s.opts.Provider.(*DeepSeekProvider)
		if record.Source == "managed" || deepSeek {
			key, err := s.cloud.key(record)
			if err != nil {
				return nil, nil, err // Never fall back from a missing managed file.
			}
			provider := &DeepSeekProvider{Model: record.Model, Client: s.cloud.client, frozen: true, frozenKey: key}
			secrets := []string{key}
			// The preserved initial host credential is still confidential after a
			// switch to managed storage. Capture it for redaction only, never as
			// a fallback identity or a retry credential.
			if record.Source == "managed" {
				if hostKey, err := s.cloud.host.secretKey(); err == nil && hostKey != key {
					secrets = append(secrets, hostKey)
				}
			}
			return provider, secrets, nil
		}
	}
	if provider, ok := s.opts.Provider.(*DeepSeekProvider); ok {
		frozen, err := provider.freeze()
		if err != nil {
			return nil, nil, err
		}
		return frozen, []string{frozen.frozenKey}, nil
	}
	var secrets []string
	if provider, ok := s.opts.Provider.(redactionSecretProvider); ok {
		var err error
		secrets, err = provider.RedactionSecrets()
		if err != nil {
			return nil, nil, err
		}
	}
	return s.opts.Provider, secrets, nil
}

func redactGeneratedSummary(summary Summary, secrets []string) Summary {
	summary.Title = redactText(summary.Title, secrets)
	summary.Summary = redactText(summary.Summary, secrets)
	summary.Answer = redactText(summary.Answer, secrets)
	summary.Topics = append([]Topic(nil), summary.Topics...)
	summary.Uncertainties = append([]string(nil), summary.Uncertainties...)
	for i := range summary.Topics {
		summary.Topics[i].Text = redactText(summary.Topics[i].Text, secrets)
	}
	for i := range summary.Uncertainties {
		summary.Uncertainties[i] = redactText(summary.Uncertainties[i], secrets)
	}
	return summary
}
