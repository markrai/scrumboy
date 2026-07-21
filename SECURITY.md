# Security policy

## Reporting a vulnerability

If you believe you have found a security vulnerability in Scrumboy, please report it **privately**:

- Prefer GitHub’s **Report a vulnerability** flow: [https://github.com/markrai/scrumboy/security](https://github.com/markrai/scrumboy/security)
- **Do not** open a public GitHub issue for security-sensitive bugs
- Allow a reasonable time for assessment and a fix before any public disclosure

We appreciate reports that help keep self-hosted deployments safer.

## Security scanning and monitoring

Scrumboy uses multiple complementary security tools which intentionally overlap. Different scanners use different vulnerability databases, reachability analysis, dependency resolution, and container-package detection. 


| Tool                        | Purpose                                                  |
| --------------------------- | -------------------------------------------------------- |
| **Snyk**                    | Dependency and container vulnerability analysis          |
| **OSV-Scanner**             | Open-source dependency advisory scanning                 |
| **Trivy**                   | Filesystem and container security scanning               |
| **Dependabot**              | Automated dependency monitoring and update pull requests |
| **govulncheck**             | Reachability-aware Go vulnerability analysis             |
| **OpenSSF Scorecard**       | Repository and software supply-chain security assessment |
| **GitHub dependency graph** | Native dependency and security advisory monitoring       |


Security tooling helps identify known issues and reduce risk, but new vulnerabilities, undisclosed flaws, configuration errors, and implementation defects can still exist despite clean scan results.

## Supported versions

Security fixes are applied on the current development line and published in releases as described in `[CHANGELOG.md](CHANGELOG.md)`. Older release tags are not guaranteed to receive backports unless a release notes entry says otherwise. Self-hosted operators should plan to upgrade to a maintained release.

## Disclosure expectations

- Private reports are handled through GitHub Security advisories when possible.
- Coordinated disclosure is preferred: share enough detail to reproduce the issue, and wait for a fix or an agreed timeline before publishing exploit details.
- Credit for reporters can be arranged when a fix ships, unless anonymity is requested.



## Technical security overview

For a technical description of Scrumboy’s authentication, authorization, data protection, security scanning, supply-chain controls, and deployment assumptions, see [Security architecture and practices](docs/security.md).