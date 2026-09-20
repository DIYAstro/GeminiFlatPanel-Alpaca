// Every third-party module/asset that actually ships inside the built binary or web UI
// bundle - i.e. it was verified to be compiled/bundled in, not just a build-time tool.
// Build-only tooling (Vite, Sass) and go.sum-only entries that nothing in this repo
// actually imports (stretchr/testify, davecgh/go-spew, pmezard/go-difflib,
// gopkg.in/yaml.v3) are intentionally excluded, since they never reach the shipped
// artifact. Versions/licenses were verified against the actual vendored LICENSE files
// in the local Go module cache and node_modules, not guessed.
export const thirdPartyLicenses = [
    {
        category: 'Go (backend binary)',
        items: [
            { name: 'fyne.io/systray', version: 'v1.11.0', license: 'Apache-2.0', url: 'https://pkg.go.dev/fyne.io/systray?tab=licenses' },
            { name: 'github.com/go-toast/toast', version: 'v0.0.0-20190211030409-01e6764cf0a4', license: 'MIT', url: 'https://pkg.go.dev/github.com/go-toast/toast?tab=licenses' },
            { name: 'github.com/gorilla/websocket', version: 'v1.5.3', license: 'BSD-2-Clause', url: 'https://pkg.go.dev/github.com/gorilla/websocket?tab=licenses' },
            { name: 'go.bug.st/serial', version: 'v1.6.0', license: 'BSD-3-Clause', url: 'https://pkg.go.dev/go.bug.st/serial?tab=licenses' },
            { name: 'golang.org/x/sys', version: 'v0.36.0', license: 'BSD-3-Clause', url: 'https://pkg.go.dev/golang.org/x/sys?tab=licenses' },
            { name: 'github.com/creack/goselect', version: 'v0.1.2', license: 'MIT', url: 'https://pkg.go.dev/github.com/creack/goselect?tab=licenses' },
            { name: 'github.com/godbus/dbus/v5', version: 'v5.1.0', license: 'BSD-2-Clause', url: 'https://pkg.go.dev/github.com/godbus/dbus/v5?tab=licenses' },
            { name: 'github.com/nu7hatch/gouuid', version: 'v0.0.0-20131221200532-179d4d0c4d8d', license: 'MIT', url: 'https://pkg.go.dev/github.com/nu7hatch/gouuid?tab=licenses' },
        ],
    },
    {
        category: 'Frontend (JavaScript)',
        items: [
            { name: 'Vue.js', version: '3.5.26', license: 'MIT', url: 'https://www.npmjs.com/package/vue' },
            { name: 'Pinia', version: '3.0.4', license: 'MIT', url: 'https://www.npmjs.com/package/pinia' },
            { name: 'Chart.js', version: '4.5.1', license: 'MIT', url: 'https://www.npmjs.com/package/chart.js' },
            { name: '@kurkle/color', version: '0.3.4', license: 'MIT', url: 'https://www.npmjs.com/package/@kurkle/color' },
            { name: 'chartjs-plugin-zoom', version: '2.2.0', license: 'MIT', url: 'https://www.npmjs.com/package/chartjs-plugin-zoom' },
            { name: 'vue-chartjs', version: '5.3.3', license: 'MIT', url: 'https://www.npmjs.com/package/vue-chartjs' },
            { name: 'hammer.js', version: '2.0.8', license: 'MIT', url: 'https://www.npmjs.com/package/hammerjs' },
        ],
    },
    {
        category: 'Fonts & Icons',
        items: [
            { name: 'Outfit (font family)', version: '—', license: 'SIL Open Font License 1.1', url: 'https://fonts.google.com/specimen/Outfit/license' },
            { name: 'RemixIcon', version: 'v4.9.0 (subset, self-hosted)', license: 'Remix Icon License 1.0', url: 'https://github.com/Remix-Design/RemixIcon/blob/master/License' },
        ],
    },
]
