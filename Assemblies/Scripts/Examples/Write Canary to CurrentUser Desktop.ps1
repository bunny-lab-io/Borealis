# Dynamically get the current user's Desktop path
$desktopPath = [Environment]::GetFolderPath('Desktop')

# Define the file path relative to the Desktop
$filePath = Join-Path $desktopPath "Canary.txt"

# Write some content into the file
"USER Canary is alive." | Out-File -FilePath $filePath -Encoding UTF8
