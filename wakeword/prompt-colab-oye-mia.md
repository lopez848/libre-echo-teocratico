Quiero entrenar en este cuaderno de Google Colab una palabra de activación personalizada con openWakeWord. Hazlo paso a paso, con celdas listas para ejecutar, y explícame brevemente qué hace cada una.

## Objetivo
- Frase: "Oye Mia" (en español, se pronuncia "oye mía").
- Resultado: UN archivo ONNX con el clasificador, compatible con openWakeWord estándar. Lo voy a usar en un parlante que ya tiene los modelos `melspectrogram.onnx` y `embedding_model.onnx` originales de openWakeWord.
- Requisito exacto del clasificador: entrada `[1, 16, 96]` (16 vectores de embedding de 96 valores), salida `[1, 1]` (probabilidad 0 a 1), opset ONNX 13 o compatible. Es el mismo formato que `alexa_v0.1.onnx`.

## Cómo entrenarlo
1. Parte del método oficial: el cuaderno `notebooks/automatic_model_training.ipynb` del repositorio github.com/dscripka/openWakeWord. Si alguna dependencia de ese cuaderno ya no instala (es de 2023), corrige las versiones o usa como referencia github.com/alfiedennen/openwakeword-colab-2026. No cambies el extractor de características: tiene que ser el melspectrogram + embedding estándar de openWakeWord.
2. Usa GPU si está disponible (Entorno de ejecución → Cambiar tipo → T4).
3. IMPORTANTE, idioma: los ejemplos positivos tienen que estar en ESPAÑOL. El generador por defecto (piper-sample-generator con LibriTTS) solo habla inglés y pronunciaría mal "Oye Mia". Genera los positivos con varias voces Piper en español de huggingface.co/rhasspy/piper-voices:
   - es_MX-claude-high, es_MX-ald-medium, es_AR-daniela-high, es_ES-davefx-medium, es_ES-sharvard-medium (tiene 2 hablantes), es_ES-carlfm-x_low, es_ES-mls_9972-low, es_ES-mls_10246-low.
   - Variantes de texto: "oye mía", "oye, mía", "oye mia", "¡oye mía!", "oye mía?".
   - Varía la velocidad (length_scale de 0.8 a 1.3), noise_scale y noise_w para tener muchas versiones por voz. Meta: al menos 5 000 clips positivos.
4. Negativos adversarios en español (frases parecidas que NO deben activarlo), generados con las mismas voces: "oye mira", "hoy mía", "oye tía", "oye María", "oye mamá", "mía", "oye", "mira", "o sea mía", "voy a ir", "lo mío", "joya mía".
5. Aumenta los datos como el cuaderno oficial: respuestas de impulso de salas (RIR) y ruido de fondo (música, conversación, ruido de casa), con la frase en distintos volúmenes.
6. Entrena con los parámetros del cuaderno oficial (unos 20 000 a 50 000 pasos), priorizando pocas activaciones falsas.

## Validación (muéstrame los resultados)
- Carga el ONNX resultante con `openwakeword.Model(wakeword_models=[ruta], inference_framework="onnx")` y pruébalo con clips positivos y negativos que NO se usaron en el entrenamiento.
- Muéstrame: tasa de aciertos con "oye mía", activaciones falsas por hora con audio normal en español, y el umbral recomendado.
- Confirma con `onnx` que la entrada es `[1, 16, 96]` y la salida `[1, 1]`.

## Entrega
- Guarda el clasificador como `oye_mia.onnx` y descárgalo a mi computadora (`files.download`).
- Dime el umbral recomendado.
