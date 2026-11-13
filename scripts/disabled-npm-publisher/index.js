// Vartovyi publishes GitHub releases and container images, never npm packages.
// Replacing the unused publisher removes npm's bundled dependency tree. Fail
// explicitly if a configuration change accidentally enables npm publishing.
export function verifyConditions() {
  throw new Error('npm publishing is disabled for Vartovyi; use the GitHub and container release plugins');
}
