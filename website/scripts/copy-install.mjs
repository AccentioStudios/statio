// Copy the canonical install.sh (repo root) into public/ so it is served from the docs
// site at https://statio.accentio.dev/install.sh. Runs as part of `npm run build`, so the
// hosted copy never drifts from the source of truth. cwd is the website/ dir when npm runs it.
import { copyFileSync } from 'node:fs';

copyFileSync('../install.sh', 'public/install.sh');
console.log('copied ../install.sh -> public/install.sh');
