# Mobile

The resident app is Flutter, in its own repository (`openbekci-mobile`, MPL-2.0), Android first. It works against **any OpenBekci instance**, the hosted service or a self-hosted server: at first launch the resident scans the site's QR code or enters its address, and the app reads the server's discovery document (`/.well-known/openbekci.json`).

## What residents can do

Sign in (SMS code, e-mail link, password or an invitation), claim a unit, see the statement per debtor side with the late fee accrued to today (and a PDF), pay by bank transfer and declare it, pay by card when the site offers it, open tickets with photos, read announcements, browse documents (including the unit's private folder), manage notifications and the profile; book facilities, pre-register visitors with a shareable QR code, approve or deny visitors from a notification, follow parcels; see the assembly agenda and call letter, answer pre-meeting polls and check in by QR code; and open the web app from the app.

## For the server operator

- **HTTPS is required.** A private CA works: the app trusts user-installed certificates. See [No public certificate](install/compose.md#no-public-certificate).
- **Push** needs a [push relay](operate/relay.md). Without push, or when the relay is unreachable, the app falls back to periodic fetch.
- **Compatibility:** the server states the minimum app version in its discovery document, and the app supports the previous two server minor versions. Release notes give the range.
- Sign-in by SMS needs an SMS provider on your server ([Install](install/compose.md)).

## Building the app

Flutter 3.47 and JDK 17 with the Android SDK; the repository README has the commands for the `dev` and `prod` flavors. The app is built against a pinned copy of the OpenAPI spec, never the server's working tree. A white-label build needs its own application id and, for push, a [relay of your own](operate/relay.md#run-your-own-relay).
