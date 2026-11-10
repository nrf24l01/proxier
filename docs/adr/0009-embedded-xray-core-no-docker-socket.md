# Proxy tests and config validation use embedded xray-core; Proxier never gets the Docker socket

Proxier embeds xray-core to test endpoints as a client, browse through a server during discovery and validate xray configurations. Other validators use Go libraries too. Compose uses compose-go, shell syntax uses `mvdan.cc/sh`, and nginx syntax uses nginx-go-crossplane. The nginx parser receives each rendered file in the context it will have inside the container.

Running validators in temporary containers would require access to the Docker socket. That would give the public-facing Proxier service root access to its host. Including nginx and bash in the image would also prevent the image from remaining distroless. The Go parsers avoid both requirements.

## Consequences

- The binary is larger because of xray-core, and xray updates come with Proxier releases.
- The nginx validation is a parse, not a run. It catches syntax errors, unknown directives, directives in the wrong context and wrong argument counts. It does not catch what only a running nginx finds (a missing certificate file, an upstream that does not resolve, a duplicate `listen`). The smoke test after provisioning remains the real check.
- The shell validation is a bash syntax check; it does not run ShellCheck.
