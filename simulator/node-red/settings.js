'use strict'

module.exports = {
  flowFile: 'flows.json',
  uiPort: process.env.PORT || 1880,
  uiHost: '0.0.0.0',
  userDir: '/data',
  functionGlobalContext: {
    simulator: require('/data/lib/simulator')
  },
  editorTheme: {
    projects: { enabled: false }
  },
  logging: {
    console: {
      level: process.env.NODE_RED_LOG_LEVEL || 'info',
      metrics: false,
      audit: false
    }
  },
  diagnostics: { enabled: false },
  runtimeState: { enabled: false }
}
