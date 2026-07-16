module github.com/stohirov/suren/backend/window

go 1.26

require (
	github.com/hajimehoshi/ebiten/v2 v2.9.9
	github.com/stohirov/suren v0.0.0
)

require github.com/cogentcore/webgpu v0.23.0 // indirect

require (
	github.com/ebitengine/gomobile v0.0.0-20250923094054-ea854a63cce1 // indirect
	github.com/ebitengine/hideconsole v1.0.0 // indirect
	github.com/ebitengine/purego v0.9.0 // indirect
	github.com/jezek/xgb v1.1.1 // indirect
	github.com/stohirov/suren/backend/gpu v0.0.0
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/sys v0.36.0 // indirect
)

replace github.com/stohirov/suren => ../..

replace github.com/stohirov/suren/backend/gpu => ../gpu
