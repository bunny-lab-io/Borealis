////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Electron/main.js
const { app, BrowserWindow } = require('electron')
const path = require('path')
const { spawn } = require('child_process')
const http = require('http')

function startBackend() {
  // In dev, __dirname is ElectronApp/, so base= one level up (project root).
  // In prod, process.resourcesPath points at the unpacked resources folder.
  const base = app.isPackaged
    ? process.resourcesPath
    : path.resolve(__dirname, '..')

  // Path to your bundled venv python.exe (or python3 on Unix)
  const pythonExe = path.join(
    base,
    'Server',
    'Scripts',
    process.platform === 'win32' ? 'python.exe' : 'python3'
  )

  // Path to the Flask server entrypoint
  const serverScript = path.join(base, 'Server', 'Borealis', 'server.py')

  console.log(`[backend] Launching: ${pythonExe} ${serverScript}`)
  const proc = spawn(pythonExe, [serverScript], {
    cwd: path.dirname(serverScript),
    stdio: ['ignore', 'pipe', 'pipe']
  })

  proc.stdout.on('data', (data) => {
    console.log(`[backend stdout] ${data.toString().trim()}`)
  })
  proc.stderr.on('data', (data) => {
    console.error(`[backend stderr] ${data.toString().trim()}`)
  })
  proc.on('exit', (code) => {
    console.log(`[backend] exited with code ${code}`)
  })

  return proc
}

function waitForServer(url, timeout = 10000) {
  return new Promise((resolve, reject) => {
    const start = Date.now()
    ;(function attempt() {
      http.get(url, (res) => {
        if (res.statusCode >= 200 && res.statusCode < 400) {
          resolve()
        } else if (Date.now() - start > timeout) {
          reject(new Error('Server did not start in time'))
        } else {
          setTimeout(attempt, 200)
        }
      }).on('error', () => {
        if (Date.now() - start > timeout) {
          reject(new Error('Server did not start in time'))
        } else {
          setTimeout(attempt, 200)
        }
      })
    })()
  })
}

async function createWindow() {
  const win = new BrowserWindow({
    width: 1280,
    height: 800,
    webPreferences: { nodeIntegration: false }
  })

  if (process.env.NODE_ENV === 'development') {
    // If you still run CRA dev server for debugging
    win.loadURL('http://localhost:3000')
  } else {
    // Load the built CRA files
    const indexPath = path.join(__dirname, 'renderer', 'index.html')
    win.loadFile(indexPath)
  }
}

app.whenReady().then(async () => {
  const backend = startBackend()

  try {
    // Wait for Flask (default port 5000)
    await waitForServer('http://localhost:5000')
    await createWindow()
  } catch (err) {
    console.error(err)
    app.quit()
    return
  }

  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) {
      createWindow()
    }
  })
})

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') {
    app.quit()
  }
})
