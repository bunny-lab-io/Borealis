# Updating the Engine
You will want to keep the engine itself up-to-date, and that process currently is manually-driven.  You can do so by following the instructions on this page.

```sh
cd /opt/Borealis

# Pull Down Changed Engine Staging Files
git pull --ff-only

# Re-Deploy the Updated Engine Containers
./Engine.sh deploy prod
```