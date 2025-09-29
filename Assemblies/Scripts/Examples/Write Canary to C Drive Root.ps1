# Define the file path
$filePath = "C:\Canary.txt"

# Write some content into the file
"SYSTEM Canary is alive." | Out-File -FilePath $filePath -Encoding UTF8
