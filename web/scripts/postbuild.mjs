// postbuild: vite build очищает dist, а web/dist/placeholder.txt закоммичен,
// чтобы `go build` с go:embed всегда работал на чистом клоне. Восстанавливаем.
import { writeFileSync } from "node:fs";

writeFileSync(
  new URL("../dist/placeholder.txt", import.meta.url),
  "placeholder: настоящая сборка dist присутствует (go:embed держит каталог ненулевым)\n",
);
console.log("postbuild: dist/placeholder.txt восстановлен");
