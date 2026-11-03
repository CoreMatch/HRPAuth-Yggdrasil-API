# Yggdrasil API WebUI SDK

This SDK package provides the Yggdrasil-related management interfaces for the HRPAuth WebUI.

## Features

- **Mojang Binding**: Manage Mojang account binding for username-based authentication.
- **Game Profile**: Create one game profile with a random UUID, a custom UUID, or a UUID looked up from a premium Minecraft username.
- **Texture Management**: Direct upload and management of player skins and capes.

## Integration

This package is designed to be aggregated by `HA-WebUI-SDKHandler`. It relies on the host application's core utilities (API request, config, cookies, etc.) via relative imports.

## Routes

For the currently declared routes and profile cards, see [`manifest.json`](manifest.json). The mounting contract — how routes, dashboard entries and profile cards are aggregated into the host WebUI at build time — is defined in HA-Contract [`docs/dev/HRPAuth/sdk-package.md`](https://github.com/CoreMatch/HA-Contract/blob/main/docs/dev/HRPAuth/sdk-package.md).
