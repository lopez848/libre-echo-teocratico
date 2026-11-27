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
T7 [S7] Emparejar del hardware real: TV de sala TCL (247) [x HECHO, aceptado
    con "Siempre permitir" y verificado desde el Echo, 6 comandos seguidos sin
    fallas tras el arreglo de la firma (L5)], Chromecast (233) [x Verificado:
    5555 cerrado → fase 2 por Cast], Fire TV [ ] pendiente: no apareció en la
    red; necesita que esté encendido o su IP (el mismo código ADB funcionará
    igual).
T8 [S2,S6] Firma ADB al contrato real de adbd (SHA-1 prehasheado) — inline.
    [x] Hecho (L5). RED: "crypto/rsa: verification error". GREEN: suite
    completa + 6 comandos reales en el TCL, 0 errores.

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
L3 2026-11-27: prueba END-TO-END en hardware real con el cliente del puente
   (commit 010f709): KEYCODE_WAKEUP + monkey YouTube → el TV despertó y abrió
   YouTube ("Events injected: 1" ... "lanzado"). Commit de la unidad: 010f709.
   Previo a desplegar en el Echo: crear /data/libreecho/local/puente-tvs.json
   (ejemplo: [{"name":"Sala","host":"192.168.1.247"}]) y aceptar el diálogo de
   depuración en cada TV UNA vez (la primera conexión desde el Echo presentará
   su clave nueva). Chromecast 233 queda para fase 2 vía Cast; Fire TV se suma
   solo con su IP/aparecer en la red. Pendiente revisión de riesgo (RDD).
L4 2026-11-27 DESPLIEGUE en el Echo (biscuit, 192.168.1.211):
   - `go test ./...` en puente con la cadena parcheada: ok puente 7.060s,
     ok puente/hap 0.568s.
   - Binario ARMv7 construido con ~/.local/share/go-echo/go1.27.4
     (CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 -buildvcs=false), 13 MB.
   - Push a services/puente/puente.new → respaldo `puente.bak-20261127` →
     chmod 755 → mv → kill. El supervisor lo reinició: service.puente=running,
     pid 3272, log "puente listening on 127.0.0.1:18080".
   - Config creada: /data/libreecho/local/puente-tvs.json
     `[{"name":"Sala","host":"192.168.1.247"}]` (644). Sin error "tv:" en el log,
     así que cargó bien.
   - Clave ADB del puente generada sola en el primer uso:
     /data/libreecho/local/adbkey y adbkey.pub (600, root).
   - Prueba por el endpoint del puente (POST /v1/chat/completions con la clave
     del aparato, "prende el TV de la sala"): el modelo llamó la herramienta y
     el puente respondió "No alcanzo el TV de Sala."
   LIMITACIÓN CONFIRMADA: el TCL 192.168.1.247 estaba fuera de la red (ARP
   FAILED desde el Echo y desde la PC; ping 100% perdido en los dos). En
   reposo profundo el TCL suelta el Wi-Fi, así que ADB no puede prenderlo desde
   apagado: "prender la pantalla" solo sirve desde reposo con red activa. Si el
   TV tiene la opción de red en reposo, hay que activarla. No hay Wake-on-LAN
   en el puente (el paquete `wol.go` es la Biblioteca en Línea, no Wake-on-LAN).
   PENDIENTE para cerrar S2/S7: con el TV encendido, repetir la prueba y
   ACEPTAR UNA VEZ en la pantalla del TV el diálogo de depuración, porque el
   Echo presenta una clave nueva (la de esta PC no se copió a propósito).
   Rollback del puente: `cd /data/libreecho/local/services/puente && cp -p
   puente.bak-20261127 puente && kill $(pidof puente)`.
L5 2026-11-27 DEFECTO REAL encontrado al verificar en hardware, no era el TV:
   Síntoma: los comandos funcionaban unas veces y otras no ("No alcanzo el TV"),
   y una pausa de 45 s parecía "arreglarlo".
   Descartado con medición, no con suposiciones:
   - El TV estaba en la red y 5555 abierto en cada falla (ping 6.9 ms).
   - Sonda sin claves (solo CNXN, 15 conexiones seguidas): el TV contestó AUTH
     las 15 veces, así que adbd NO limita las conexiones seguidas.
   - El registro nuevo (L4) dio la pista exacta: el saludo llegaba hasta enviar
     la línea de clave pública y ahí el TV se quedaba callado, o sea que la
     FIRMA venía siendo rechazada siempre.
   CAUSA: `sign()` hacía SHA-256 del token y firmaba con DigestInfo de SHA-256.
   adbd verifica con `RSA_verify(NID_sha1, token, token_size, ...)`, y OpenSSL
   con NID_sha1 toma esos bytes como un resumen SHA-1 YA calculado. Fuente:
   adb_auth_host.cpp (`RSA_sign(NID_sha1, token, token_size, ...)`) y
   daemon/auth.cpp en AOSP. El token real medido en el TCL: 20 bytes, el tamaño
   de un SHA-1. La T1 del 2026-11-27 anotó "RSA-SHA256" y era incorrecto; la
   prueba TestADBAuthSignature verificaba nuestra propia firma contra nuestro
   propio SHA-256, así que el error no se veía.
   Por qué parecía intermitente: con la firma rechazada, cada comando caía en la
   ruta de la clave pública, que en Android pasa por el diálogo de la pantalla.
   Funcionaba solo cuando el sistema aprobaba la clave sin preguntar.
   ARREGLO (commit siguiente): firmar el token de 20 bytes tal cual
   (`rsa.SignPKCS1v15(..., crypto.SHA1, token)`), con guardia de tamaño.
   PRUEBAS: TestADBAuthSignature reescrito al contrato de adbd (RED observado:
   "crypto/rsa: verification error") y TestADBSignRejectsOddToken nuevo.
   go test ./... ok puente 7.199s, ok puente/hap. go vet limpio.
   HARDWARE: desplegado en el Echo; 6 comandos seguidos (volumen arriba,
   silenciar, inicio, abrir YouTube, pausa, volumen abajo) todos correctos, 0
   errores de TV desde el arranque, sin tocar la pantalla del TV, y el tiempo
   por comando bajó de 11-34 s a 0.8-2.4 s. En pantalla quedó
   com.google.android.youtube.tv. T7 cerrado para el TV de sala.
   Anotado, sin tocar: el puente abre una conexión ADB nueva por comando;
   guardarla abierta ahorraría el saludo de cada vez.
L6 2026-11-27: el dueño confirmó por su cuenta: "ya lo probe funciona bien".
   S1-S6 cerrados y verificados en hardware. Queda abierto solo T7 para el Fire
   TV (falta su IP o que aparezca en la red) y el Chromecast 233 por Cast en una
   fase 2. Rama feat/tv-control sin subir: subir y abrir PR es decisión suya.
