# A server has a country, not a location

A server stores its country as an ISO 3166-1 alpha-2 code. The New server page offers a searchable picker and suggests the IP's country. Proxier derives the country name from the English and Russian lists in `servers/country`, and the flag from regional-indicator letters.

The former location record duplicated the country code, name and flag and needed its own add, rename and delete rules. Removing the Locations page and table avoids storing those values twice.

- A new server is named `<code in lower case>-<number>` (`nl-2`); the number is one more than the highest its name prefix has ever used, read from the servers themselves (a server row is never deleted, so a number stays taken).
- The display name apps show is `{flag} {English country name} {number}`, in every link whatever its language.
- The migration gives each server its location's country (a withdrawn code the old picker offered, such as `UK`, becomes its successor, `GB`) and drops the table. Servers keep their names: a server of a location coded `ams` stays `ams-1`, and `ams` stays its name prefix. The locations as they were entered are kept as JSON in `servers_meta` (`locations.archive`) only so that a rollback gives them back.

## Considered options

- **Keep locations, make them optional**: still a second thing to name and keep consistent with the country, for no data of its own.
- **Store the name and flag on the server**: derived values that can drift from the code; nothing needs them stored.

## Consequences

- A custom location name ("Amsterdam") is gone from the UI and, after the server's next deployment refreshes its endpoints, from the apps' display names, which become the country's English name.
- Two locations of one country (`nl` and `ams`, both NL) both show as Netherlands; their numbers were counted apart, so `nl-1` and `ams-1` can both read "🇳🇱 Netherlands 1" in an app.
- Stack templates see `.Server.Country` (`Code`, `Name`, `Flag`); `.Server.Location` stays, derived, so versions published before still render. The hostname pattern's placeholder is `{country}`; `{location}` still works in a saved pattern.
- `location.*` events are no longer recorded; older ones still show in Activity, named from the event that added the location.
