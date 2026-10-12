# Yggdrasil API WebUI SDK

This SDK package provides the Minecraft skin, cape, and Yggdrasil-related management interfaces for the HRPAuth WebUI.

## Features

- **Skin Library**: Browse, search, and apply textures from the configured skin source.
- **Mojang Binding**: Manage Mojang account binding for username-based authentication.
- **Texture Management**: Direct upload and management of player skins and capes.

## Integration

This package is designed to be aggregated by `HA-WebUI-SDKHandler`. It relies on the host application's core utilities (API request, config, cookies, etc.) via relative imports.

## Routes

- `/skinlib`: The main skin library page.
- `/mojang-bind`: Mojang account binding management.
- `/texture-management`: Personal skin and cape management.
