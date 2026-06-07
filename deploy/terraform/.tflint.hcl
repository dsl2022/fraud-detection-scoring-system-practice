# tflint config. The "terraform" ruleset is bundled (no plugin download), so
# --init is a no-op and CI stays hermetic. The "recommended" preset catches
# unused declarations, naming conventions, deprecated syntax, missing required
# versions, etc. Add the AWS ruleset plugin later for provider-specific checks.

plugin "terraform" {
  enabled = true
  preset  = "recommended"
}
