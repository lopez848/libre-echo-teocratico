# Control de TVs por voz (puente/tv)

Objetivo: que Mía prenda, controle y lance apps en los TVs de la casa por voz desde el Echo.
Rama: feat/tv-control (desde main d84f662). Test runner: `go test ./puente/`.
Delivery: ask-on-risk.

## Specs

S1. El puente lee la configuración de TVs de una bandera `-tv` con archivo JSON
    (`/data/libreecho/local/puente-tvs.json` por defecto); un archivo ausente o
    inválido deshabilita el feature sin romper el arranque.
S2. Se controla un Android TV por ADB directo sobre TCP (adbd), con claves de
    ADB del propio puente guardadas en `/data/libreecho/local/` (par retrospectivo:
    el arranque pedirá al usuario aceptar la TV una vez).
S3. Herramienta `tv_control` con acciones: abrir app (por nombre en español),
    pausar/reanudar, teclas (home, atrás, arriba/abajo/izq/der, OK), subir/bajar/
    silenciar volumen, prender/apagar la pantalla, listar apps.
S4. Si el usuario no nombra el TV y hay más de uno configurado, Mía pregunta cuál
    ("¿el TV de la sala o el de la habitación?"); con uno solo no pregunta.
S5. El texto de la herramienta y las respuestas habladas quedan en español neutro
    (mismo estilo que set_light).
S6. Protocolo ADB implementado en Go puro (sin depender de binario adb en el
    Echo), para valer también en el Fire TV.
S7. El Chromecast de la habitación ("Habitación principal", 192.168.1.233) entra
    por ADB si se puede habilitar; si no queda para una fase 2 por Cast (queda
    registrado como limitación confirmada del hardware).

## Tasks

T1 [S6] Verificar el protocolo de transporte ADB (fuente oficial) — inline.
    [x] Hecho: paquete de referencia adb_shell 0.4.4 (Apache/Google) Verificó:
    formato de 24 bytes <6I, CNXN/AUTH/OPEN/OKAY/WRTE/CLSE, AUTH token→firma
    RSA-SHA256→(línea RSAPublicKey de 524 bytes LE) sí aceptada.
T2 [S1,S2,S6] cliente ADB en puente/adb.go + clave + tv.go (config y resolución)
    [x] Hecho. Puente GREEN: handshake, firma exacta del token verificada con
    rsa.VerifyPKCS1v15, auth por RSAPublicKey, timeouts, con adbd falso
    (adb_test.go). Verificacion HARDWARE REAL (TCL 192.168.1.247): echo +
    getprop respondieron "hola-tcl / 12" sin ninguna interacción de la pantalla
    usando ~/.android/adbkey (la clave que esta PC ya tenía aceptada); la
    escritura en /data/misc/adb la bloquea SELinux pero no hace falta.
T3 [S3] Herramienta tv_control: JSON en toolSpecs, caso en el switch de
    tools.go, mapeo app-español→paquete, comandos hablados — inline.
    [x] Hecho: specs JSON (action abrir/cerrar/tecla/volumen), apps por
    tvApps + overrides del config + fragmento sobre pm list packages -3.
T4 [S4] Desambiguación de TV cuando hay varios — inline.
    [x] Hecho (pickHost): único TV no pregunta; varios sin nombre preguntan.
T5 [S5] prompt del sistema menciona tv_control — inline.
    [x] Hecho (main.go, en la lista de intenciones).
T6 [RESULTADOS] suite de tv (tv_test.go): pickHost, flujo abrir/cerrar con
    adbd falso, pregunta ambigua, inalcanzable, volumen x3 o silenciar,
    teclas prerender de pantalla (SLEEP/WAKEUP), accion/tecla desconocida,
    carga de JSON (ausente desactiva, erróneo reporta).
    [x] Hecho — go test ./puente TODO ok 7.0s (además de toda la suite
    preexistente sin regresiones).
T7 [S7] Emparejar del hardware real: TV de sala TCL (247) [x HECHO, conectó
    en directo], Chromecast (233) [x Verificado: 5555 cerrado → fase 2 por
    Cast], Fire TV [ ] pendiente: no apareció en la red; necesita que esté
    encendido o su IP (el mismo código ADB funcionará igual).

## Log

L1 2026-11-27 pedido original verbatim:
   "quiero poder controlar mis tvs buscalos tienes las ip de ellas"
   (IPs de los TVs no estaban guardadas en ningún lado; se escaneó la red).
L1b 2026-11-27: usuario enumeró sus TVs: "Fire TV Stick/cubo, google crhomecast".
L2 2026-11-27: alcance elegido por el usuario: "el mayor control que se pueda",
   y re-escaneo con los TVs encendidos.
Hallazgos (verificados en la red 192.168.1.0/24):
   - TV de sala: TCL Google TV "TV de Sala familiar", 192.168.1.247
     (adb 5555 YA autorizado desde esta PC; puertos 8008 DIAL, 8009 Cast, 6466).
   - Habitación: Chromecast with Google TV "Eureka Dongle", 192.168.1.233
     (cast 8009, androidtvremote2; adb 5555 refused — ver S7).
   - Fire TV Stick/cubo: NO apareció en el re-escaneo (queda pendiente su IP;
     puede estar profundamente apagado o en otra red).
   - El resto de hosts son celulares/Ecos/impresora/aire (no TVs).
Próximo paso: T1 → T2.
