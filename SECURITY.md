# Security Policy

## Reporting a vulnerability

Please report security vulnerabilities privately, never in a public issue.

Use GitHub's private vulnerability reporting: open the repository's **Security**
tab and click **Report a vulnerability**. That opens a private advisory only the
maintainers can see.

We aim to acknowledge a report within three working days and to keep you updated
while we investigate. When a fix is ready we coordinate a release and, with your
agreement, credit you in the advisory.

## Supported versions

keel is pre-1.0 and ships from its latest release. Security fixes land on the
latest release; older tags are not maintained.

## Scope

keel runs locally: a command-line tool plus a loopback-only browser studio. The
areas most worth a close look:

- the studio server's request handling, same-origin checks and per-session token
- the plugin trust model and per-capability grants
- how a trusted plugin's own executables are run (the subprocess boundary)
- recipe and scaffold file writes into a project

keel ships zero telemetry and makes only the network calls a command explicitly
asks for (installing a tool, fetching a recipe pack, or a plugin action you ran).
