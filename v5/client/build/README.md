# Build Assets for MyVPN Wails App

## App Icon

Place `appicon.png` in this directory (512×512 recommended).

### Generate from SVG

```bash
# Requires Inkscape or similar
inkscape -w 512 -h 512 ../frontend/src/assets/icon.svg -o appicon.png
```

### Or use a placeholder

Wails will use a default icon if none is provided. For production builds,
create a proper icon matching the MyVPN brand (purple shield, see UI-AESTHETICS.md).
