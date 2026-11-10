# Server templates are rendered by Proxier, not scripts run on the server

A template version contains a manifest and files. Proxier generates UUIDs and secret paths, renders files with Go templates, uploads them over SSH and runs the declared steps. It stores the exact configuration of every server, which lets it preview changes, redeploy, upgrade and rotate credentials. Members edit, validate and publish template versions in the UI.

## Considered options

- **Run the existing bash scripts non-interactively and parse their output**: the fastest port. But Proxier would only know what a script printed, and every later change (rotation, new users, a new decoy site) would need yet another script.
- **Ansible playbooks**: idempotent and good for ongoing changes, but Python and Ansible in the image and a second language, while the stacks are just a few files and `docker compose`.

## Consequences

- The seed template defines the initial proxy stack. Keep other templates in separate repositories and import them by zip or git URL.
- Templates must render the xray client list from `.Clients`, so that per-link credentials, if they ever arrive, are a data change and not a template rewrite.
