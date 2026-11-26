<!-- Diseño propuesto por Gentle Shell (DeepSeek V4 Flash) el 2026-11-26, revisado por Claude. Sin implementar. -->
# Cómo actualizar varios parlantes LibreEcho sin visitar cada casa (diseño, sin implementar)

## Recomendación

**Que cada parlante busque sus actualizaciones solo (modelo "tirón"), no que tú las
instales a distancia (modelo "empujón").** Un programa nuevo y muy pequeño —"ota"—
corre dentro de cada Echo, revisa un par de veces al día si publicaste una versión
nueva en GitHub Releases, y si hay algo nuevo lo **verifica con una firma digital
tuya**, lo instala con red de seguridad y **revierte solo** si falla. Tú publicas una
vez; ninguna casa de tus amigos necesita configurar nada.

## 1. Por qué tirón y no empujón

- **Empujón** (túnel inverso SSH, Tailscale, etc.) obliga a configurar algo en cada
  casa: una cuenta, una conexión permanente hacia tu equipo, algo que sobreviva a los
  reinicios del router. Si tu PC está apagado o una amiga cambia de router, nadie se
  actualiza y tú ni te enteras. Con personas no técnicas, cada punto de configuración
  es una falla futura. Tailscale (https://tailscale.com/kb/) sería la variante más
  limpia, pero añade cuentas y un tercero para algo que un simple GET resuelve.
- **Tirón** usa el camino que ya funciona: el parlante ya sale a internet por HTTPS
  todo el día (YouTube, jw.org, NVIDIA — `puente/youtube.go`, verificado en el
  dispositivo el 2026-11-25). Consultar GitHub Releases
  (https://docs.github.com/en/repositories/releasing-projects-on-github/about-releases;
  la API pública permite 60 consultas por hora y dirección IP, de sobra para 1
  consulta por hora y casa) es exactamente lo mismo desde el punto de vista de red.
  Para ti, publicar un arreglo es solo subir una versión más, y cada parlante la toma solo.

## 2. Integridad y seguridad

Cada release incluye, junto al binario, un **manifiesto** con versión, hash SHA-256,
dónde se instala y una firma.

- **Firma:** firmas el manifiesto con una llave Ed25519 que **nunca sale de tu PC**
  (sin conexión a internet). El parlante lleva grabada la llave pública y solo
  ejecuta lo firmado con ella. Dos caminos: una herramienta propia en Go con
  `crypto/ed25519` de la biblioteca estándar
  (https://pkg.go.dev/crypto/ed25519; cero dependencias, compila con tu toolchain
  parcheada del Echo) o el formato minisign
  (https://jedisct1.github.io/minisign/, también Ed25519). Recomiendo la propia: el
  dispositivo usa BusyBox y menos piezas es menos riesgo.
- **Si alguien roba o pierde control de tu cuenta de GitHub:** puede subir archivos,
  pero no puede firmar el manifiesto. Los parlantes ignoran todo lo no firmado. El
  peor daño es "no actualiza" (se queda viejo), nunca "ejecuta algo ajeno". Si tu PC
  se compromete, el arreglo (rotar la llave) es publicar una versión firmada con la llave
  vieja que instale la llave nueva; en el peor caso, se cambia a mano en cada parlante.
- **Nunca una versión más vieja:** el manifiesto firmado lleva un número de versión y el
  parlante rechaza cualquier versión menor a la que ya tiene, aunque esté bien firmada
  (así nadie puede reinstalarle una versión vieja con un fallo conocido).
- **De confianza a la firma, jamás al contenido:** hash SHA-256 contra manifiesto
  firmado, y solo entonces se instala.
- **HTTPS:** los binarios Go validan los certificados con las raíces del sistema, y
  esa validación ya funciona hoy en el dispositivo. **null:** no pude confirmar
  dónde vive el almacén de certificados en LibreEcho; no hace falta tocarlo porque
  ya se usa en producción.

## 3. Instalación segura y retroceso en el parlante

Regla dura respetada: **ota solo escribe en `/data/libreecho/local/`**; jamás toca el
arranque, las particiones ni nada del sistema.

1. Baja el binario y lo deja en `services/puente/puente.new` con permisos 755.
   Siempre guarda la versión anterior como `puente.bak`.
2. Cambio atómico: `mv puente.new puente` (renombrar en el mismo disco es atómico).
3. `kill $(pidof puente)`: el supervisor local que ya existe
   (`/usr/local/sbin/libreecho-local`) lo rehace lanzar solo. Reutiliza el
   mecanismo actual; no se instala ningún gestor que compita con él.
4. **Chequeo de salud:** durante unos minutos sondea `http://127.0.0.1:18080/health`
   (ya existe: `puente/main.go`).
5. **Retroceso automático:** si `/health` no responde, regresa `puente.bak` con otro
   `mv`, relanza y avisa por ntfy; esa versión fallida no se reintenta sola.
   Ojo con un detalle del supervisor: se rinde tras 10 relanzamientos en 10
   minutos (device.md); el chequeo debe terminar dentro de esa ventana (cifra
   concreta a validar al implementar).
6. Todo queda en el archivo de eventos que vigia ya mantiene
   (`/data/libreecho/local/vigia/events.jsonl`).

Detalle útil: no todo arreglo necesita un binario nuevo. Las constantes de los
clientes de YouTube (`ytWeb`, `ytMusic`, `ytProducer` en `puente/youtube.go`) pueden
vivir en un JSON firmado que ota también descarga: gran parte de las roturas de
YouTube se arreglarían cambiando versión de cliente, sin recompilar.

## 4. Configuración por casa que NO se comparte

Nunca en el repositorio público ni en las versiones:

- **Credenciales del puente** (`/data/libreecho/local/puente.creds`, 600): usuario y
  contraseña de cada parlante.
- **ntfy** (`/data/libreecho/local/vigia.conf`, 600): tema propio de cada parlante,
  para que los problemas de una casa no se mezclen con los de otra y nunca se
  filtren versículos ajenos. Referencia: https://docs.ntfy.sh.
- **Zona horaria** (bandera `-tz` de puente): distinta en cada casa.
- **Llave de NVIDIA** (`/data/libreecho/secrets/openai-codex.json`, 600): si decides
  compartirla entre la flota, vive solo en cada dispositivo, jamás en un release.
- El aprendizaje de gustos (`puente-taste.json`) también es dato local de cada casa.

**Cómo:** una carpeta privada aparte (NO `libre-echo-teocratico`, que es MIT y pública),
p. ej. `flota/`, con un listado de hogares: nombre, serial ADB, zona horaria, tema
ntfy. Un script de aprovisionamiento (fase 3) lo lee y genera los ficheros al dar de
alta cada parlante.

## 5. Visibilidad de la flota

- Cada alerta de vigia incluye el **nombre del parlante** (hoy no lo lleva); con
  tema propio por parlante se distingue de inmediato.
- **Latido diario:** cada parlante manda una vez al día «Mia Cocina Miguel:
  v0.14.6-p2, ok». Si un tema se queda silencioso, algo va mal aunque no haya alerta.
- Tras actualizar: «actualizado a v0.15.0» o «v0.15.0 FALLÓ, volvió a v0.14.6»; los
  eventos ya existen, solo hace falta mandar el mensaje por ntfy.
- El binario imprime su versión al arrancar en el log que vigia ya inspecciona.

## 6. Alta de un parlante nuevo

**Automatizable** (una vez por casa, script `alta.sh`): crear usuario puente y sus
credenciales locales, `vigia.conf` con `tz` y tema propio, copiar los servicios bajo
`/data/libreecho/local/services/`, ajustar `agent.json` apuntando al puente (edición
+ `/etc/init.d/libreecho-agentd.init restart`) y primer arranque de servicios.

**Riesgo, fuera del script y fuera de este proyecto:** flashear LibreEcho OS en un
Echo de segunda mano con firmware de fábrica. Referencia: `run-one-shot.sh` envuelve
el instalador. Se hace a mano, una unidad a la vez, nunca en remoto. (Solo el puntero.)

## 7. Plan por fases

| Fase | Qué | Esfuerzo | Riesgo principal |
|---|---|---|---|
| 1. MVP | "ota" en Go: baja manifiesto firmado de GitHub Releases, verifica firma y SHA-256, `mv` atómico, chequeo de salud, retroceso con `bak`. Se prueba solo en tu propio parlante (con una versión de prueba). | 1–2 fines de semana | binario nuevo que no arranca; mitigado por `bak` + chequeo |
| 2. Visibilidad | Nombre del parlante en las alertas, latido diario, aviso de actualización fallida. | ~1 día | ntfy sin salida; vigia ya encola |
| 3. Flota | `flota/` privada + `alta.sh`; 2–3 parlantes piloto de amigos de confianza. | 1 fin de semana por casa | flasheo manual (excluido por diseño) |
| 4. Duro | Arreglos solo-datos (clientes YouTube en JSON firmado), rotación de llave documentada, resumen semanal de flota. | 2–4 días | complejidad innecesaria; solo si lo anterior va bien |

**Riesgos generales:** cuenta de GitHub robada — cubierto por la firma; lo peor es no
actualizar, nunca ejecutar código ajeno. Caída de GitHub o de tu PC — el binario
instalado sigue funcionando solo. Espacio en `/data` — vigia ya avisa por debajo del
10 % y los dos binarios caben sin apuro. Go sin `eventfd` — ota compila con la misma
toolchain parcheada que puente (`tools/go-echo/setup.sh`).

## Pendiente de confirmar (null)

- Ubicación del almacén de certificados del sistema en LibreEcho (solo está
  confirmado que el HTTPS saliente ya funciona).
- Ventana exacta del supervisor (10 relanzamientos/10 min, de device.md) para fijar
  el número de minutos del chequeo de salud.
- Si GitHub Releases aplica cachés que compliquen la descarga repetida (el hash va
  dentro del manifiesto firmado, así que la confianza nunca se pone en el canal solo
  en la firma; se comprueba al implementar).
- Cómo maneja LibreEcho la hora antes de su primer ajuste (afectaría solo la puntualidad
  del latido diario, no la seguridad).
