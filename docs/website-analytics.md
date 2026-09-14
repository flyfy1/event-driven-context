# Hosted website analytics

Only `context.integ.life` loads the production GA4 web stream `G-J7NSTMB704`.
Local and other self-hosted deployments do not load the Google tag.

Page views contain a fixed site title, an empty referrer and an allowlisted page
name: landing, admin, or one of the five workspace views. Record contents, project
IDs, queries, fragment parameters and personal titles are never forwarded.
Enhanced measurement and advertising signals are disabled in this data stream.
The browser integration does not alter CLI, backend or self-hosted storage.
