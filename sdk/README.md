# Yggdrasil API WebUI SDK

This SDK package provides the Yggdrasil-related management interfaces for the HRPAuth WebUI.

## Features

- **Mojang Binding**: Manage Mojang account binding for username-based authentication.
- **Texture Management**: Direct upload and management of player skins and capes.

## Integration

This package is designed to be aggregated by `HA-WebUI-SDKHandler`. It relies on the host application's core utilities (API request, config, cookies, etc.) via relative imports.

## Routes

- `/mojang-bind`: Mojang account binding management.
- `/texture-management`: Personal skin and cape management.
- `/yggdrasil-api`: Yggdrasil API information and usage guidance.
