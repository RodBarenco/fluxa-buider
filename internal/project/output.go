package project

// SetOutput overrides the build output directory after Load, applying the
// exact same relative-path safety rule validate applies to build.output:
// the path must be relative, must not traverse outside the project, and
// must remain inside cfg.Root once resolved. On success it updates both
// Build.Output and OutputPath; on failure cfg is left unchanged.
func (cfg *Config) SetOutput(relative string) error {
	resolved, err := resolveProjectPath(cfg.Root, relative, false)
	if err != nil {
		return validationErrorWithCause("build.output", relative, err)
	}
	cfg.Build.Output = relative
	cfg.OutputPath = resolved
	return nil
}
