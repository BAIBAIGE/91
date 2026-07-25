const TRIPLE_SCREEN_COUNT = 3;
const MAX_CANVAS_DEVICE_PIXEL_RATIO = 2;

export type TripleScreenViewport = {
  x: number;
  y: number;
  width: number;
  height: number;
};

export type CanvasPixelSize = {
  width: number;
  height: number;
};

export function isPortraitVideo(videoWidth: number, videoHeight: number) {
  return (
    Number.isFinite(videoWidth) &&
    Number.isFinite(videoHeight) &&
    videoWidth > 0 &&
    videoHeight > videoWidth
  );
}

export function calculateTripleScreenViewport(
  containerWidth: number,
  containerHeight: number,
  videoWidth: number,
  videoHeight: number
): TripleScreenViewport | null {
  if (
    !isPositiveFinite(containerWidth) ||
    !isPositiveFinite(containerHeight) ||
    !isPortraitVideo(videoWidth, videoHeight)
  ) {
    return null;
  }

  const compositeWidth = videoWidth * TRIPLE_SCREEN_COUNT;
  const scale = Math.min(
    containerWidth / compositeWidth,
    containerHeight / videoHeight
  );
  const width = compositeWidth * scale;
  const height = videoHeight * scale;

  return {
    x: (containerWidth - width) / 2,
    y: (containerHeight - height) / 2,
    width,
    height,
  };
}

export function calculateCanvasPixelSize(
  cssWidth: number,
  cssHeight: number,
  devicePixelRatio: number
): CanvasPixelSize | null {
  if (!isPositiveFinite(cssWidth) || !isPositiveFinite(cssHeight)) {
    return null;
  }

  const ratio = clamp(
    isPositiveFinite(devicePixelRatio) ? devicePixelRatio : 1,
    1,
    MAX_CANVAS_DEVICE_PIXEL_RATIO
  );
  return {
    width: Math.max(1, Math.round(cssWidth * ratio)),
    height: Math.max(1, Math.round(cssHeight * ratio)),
  };
}

type TripleScreenRendererOptions = {
  container: HTMLElement;
  video: HTMLVideoElement;
  canvas: HTMLCanvasElement;
  onVisibilityChange?: (visible: boolean) => void;
  onError?: (error: Error) => void;
};

export class TripleScreenRenderer {
  private readonly container: HTMLElement;
  private readonly video: HTMLVideoElement;
  private readonly canvas: HTMLCanvasElement;
  private readonly onVisibilityChange?: (visible: boolean) => void;
  private readonly onError?: (error: Error) => void;

  private gl: WebGLRenderingContext | null = null;
  private program: WebGLProgram | null = null;
  private texture: WebGLTexture | null = null;
  private positionBuffer: WebGLBuffer | null = null;
  private textureBuffer: WebGLBuffer | null = null;
  private indexBuffer: WebGLBuffer | null = null;
  private positionLocation = -1;
  private textureLocation = -1;
  private samplerLocation: WebGLUniformLocation | null = null;
  private maxTextureSize = 0;

  private enabledValue = false;
  private visible = false;
  private destroyed = false;
  private frameCallbackID: number | null = null;
  private animationFrameID: number | null = null;
  private resizeObserver: ResizeObserver | null = null;

  constructor({
    container,
    video,
    canvas,
    onVisibilityChange,
    onError,
  }: TripleScreenRendererOptions) {
    this.container = container;
    this.video = video;
    this.canvas = canvas;
    this.onVisibilityChange = onVisibilityChange;
    this.onError = onError;

    this.canvas.hidden = true;
    this.video.addEventListener("playing", this.handlePlaying);
    this.video.addEventListener("loadeddata", this.handleFrameAvailable);
    this.video.addEventListener("seeked", this.handleFrameAvailable);
    this.video.addEventListener("emptied", this.handleVideoEmptied);
    this.canvas.addEventListener("webglcontextlost", this.handleContextLost);
    this.canvas.addEventListener(
      "webglcontextrestored",
      this.handleContextRestored
    );

    if (typeof ResizeObserver !== "undefined") {
      this.resizeObserver = new ResizeObserver(this.handleResize);
      this.resizeObserver.observe(this.container);
    }
    window.addEventListener("resize", this.handleResize);
  }

  get enabled() {
    return this.enabledValue;
  }

  enable() {
    if (
      this.destroyed ||
      !isPortraitVideo(this.video.videoWidth, this.video.videoHeight)
    ) {
      return false;
    }
    if (!this.ensureResources()) {
      return false;
    }

    this.enabledValue = true;
    this.resizeCanvas();
    if (this.video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA) {
      if (!this.drawFrame()) return false;
    }
    this.scheduleFrame();
    return this.enabledValue;
  }

  disable() {
    this.enabledValue = false;
    this.cancelFrame();
    this.setVisible(false);
  }

  resize() {
    if (!this.enabledValue) return;
    this.resizeCanvas();
    if (this.video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA) {
      this.drawFrame();
    }
  }

  destroy() {
    if (this.destroyed) return;
    this.destroyed = true;
    this.disable();
    this.video.removeEventListener("playing", this.handlePlaying);
    this.video.removeEventListener("loadeddata", this.handleFrameAvailable);
    this.video.removeEventListener("seeked", this.handleFrameAvailable);
    this.video.removeEventListener("emptied", this.handleVideoEmptied);
    this.canvas.removeEventListener(
      "webglcontextlost",
      this.handleContextLost
    );
    this.canvas.removeEventListener(
      "webglcontextrestored",
      this.handleContextRestored
    );
    this.resizeObserver?.disconnect();
    window.removeEventListener("resize", this.handleResize);
    this.releaseResources(true);
    this.canvas.remove();
  }

  private readonly handlePlaying = () => {
    this.scheduleFrame();
  };

  private readonly handleFrameAvailable = () => {
    if (!this.enabledValue) return;
    this.resizeCanvas();
    if (this.video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA) {
      this.drawFrame();
    }
    this.scheduleFrame();
  };

  private readonly handleVideoEmptied = () => {
    this.disable();
  };

  private readonly handleResize = () => {
    this.resize();
  };

  private readonly handleContextLost = (event: Event) => {
    event.preventDefault();
    const wasEnabled = this.enabledValue;
    this.enabledValue = false;
    this.cancelFrame();
    this.setVisible(false);
    this.releaseResources(false);
    if (wasEnabled) {
      this.onError?.(new Error("WebGL context lost"));
    }
  };

  private readonly handleContextRestored = () => {
    this.releaseResources(false);
  };

  private ensureResources() {
    if (
      this.gl &&
      this.program &&
      this.texture &&
      this.positionBuffer &&
      this.textureBuffer &&
      this.indexBuffer
    ) {
      return true;
    }

    let gl: WebGLRenderingContext | null = null;
    let vertexShader: WebGLShader | null = null;
    let fragmentShader: WebGLShader | null = null;
    try {
      gl =
        this.canvas.getContext("webgl", {
          alpha: false,
          antialias: false,
          preserveDrawingBuffer: true,
          powerPreference: "high-performance",
        }) ||
        (this.canvas.getContext(
          "experimental-webgl",
          {
            alpha: false,
            antialias: false,
            preserveDrawingBuffer: true,
            powerPreference: "high-performance",
          } as WebGLContextAttributes
        ) as WebGLRenderingContext | null);
      if (!gl) {
        throw new Error("WebGL unavailable");
      }
      this.gl = gl;

      vertexShader = compileShader(
        gl,
        gl.VERTEX_SHADER,
        `
          attribute vec2 aPosition;
          attribute vec2 aTexCoord;
          varying vec2 vTexCoord;
          void main() {
            gl_Position = vec4(aPosition, 0.0, 1.0);
            vTexCoord = aTexCoord;
          }
        `
      );
      fragmentShader = compileShader(
        gl,
        gl.FRAGMENT_SHADER,
        `
          precision mediump float;
          varying vec2 vTexCoord;
          uniform sampler2D uSampler;
          void main() {
            gl_FragColor = texture2D(uSampler, vTexCoord);
          }
        `
      );
      const program = linkProgram(gl, vertexShader, fragmentShader);
      this.program = program;

      const positionBuffer = gl.createBuffer();
      if (!positionBuffer) {
        throw new Error("WebGL resource allocation failed");
      }
      this.positionBuffer = positionBuffer;

      const textureBuffer = gl.createBuffer();
      if (!textureBuffer) {
        throw new Error("WebGL resource allocation failed");
      }
      this.textureBuffer = textureBuffer;

      const indexBuffer = gl.createBuffer();
      if (!indexBuffer) {
        throw new Error("WebGL resource allocation failed");
      }
      this.indexBuffer = indexBuffer;

      const texture = gl.createTexture();
      if (!texture) {
        throw new Error("WebGL resource allocation failed");
      }
      this.texture = texture;

      const positions = new Float32Array([
        -1, -1, -1 / 3, -1, -1, 1, -1 / 3, 1,
        -1 / 3, -1, 1 / 3, -1, -1 / 3, 1, 1 / 3, 1,
        1 / 3, -1, 1, -1, 1 / 3, 1, 1, 1,
      ]);
      const textureCoordinates = new Float32Array([
        0, 1, 1, 1, 0, 0, 1, 0,
        0, 1, 1, 1, 0, 0, 1, 0,
        0, 1, 1, 1, 0, 0, 1, 0,
      ]);
      const indices = new Uint16Array([
        0, 1, 2, 1, 2, 3,
        4, 5, 6, 5, 6, 7,
        8, 9, 10, 9, 10, 11,
      ]);

      gl.bindBuffer(gl.ARRAY_BUFFER, positionBuffer);
      gl.bufferData(gl.ARRAY_BUFFER, positions, gl.STATIC_DRAW);
      gl.bindBuffer(gl.ARRAY_BUFFER, textureBuffer);
      gl.bufferData(gl.ARRAY_BUFFER, textureCoordinates, gl.STATIC_DRAW);
      gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, indexBuffer);
      gl.bufferData(gl.ELEMENT_ARRAY_BUFFER, indices, gl.STATIC_DRAW);

      gl.bindTexture(gl.TEXTURE_2D, texture);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);

      const positionLocation = gl.getAttribLocation(program, "aPosition");
      const textureLocation = gl.getAttribLocation(program, "aTexCoord");
      const samplerLocation = gl.getUniformLocation(program, "uSampler");
      if (
        positionLocation < 0 ||
        textureLocation < 0 ||
        samplerLocation === null
      ) {
        throw new Error("WebGL shader bindings unavailable");
      }

      this.positionLocation = positionLocation;
      this.textureLocation = textureLocation;
      this.samplerLocation = samplerLocation;
      this.maxTextureSize = Number(gl.getParameter(gl.MAX_TEXTURE_SIZE));
      if (!isPositiveFinite(this.maxTextureSize)) {
        throw new Error("WebGL texture size limit unavailable");
      }
      if (gl.getError() !== gl.NO_ERROR) {
        throw new Error("WebGL resource initialization failed");
      }
      return true;
    } catch (error) {
      this.releaseResources(true);
      this.reportFailure(error);
      return false;
    } finally {
      if (gl && !gl.isContextLost()) {
        if (vertexShader) gl.deleteShader(vertexShader);
        if (fragmentShader) gl.deleteShader(fragmentShader);
      }
    }
  }

  private resizeCanvas() {
    const size = calculateCanvasPixelSize(
      this.container.clientWidth,
      this.container.clientHeight,
      window.devicePixelRatio
    );
    if (!size) return;
    if (this.canvas.width !== size.width) this.canvas.width = size.width;
    if (this.canvas.height !== size.height) this.canvas.height = size.height;
  }

  private drawFrame() {
    if (
      !this.enabledValue ||
      !this.gl ||
      !this.program ||
      !this.texture ||
      !this.positionBuffer ||
      !this.textureBuffer ||
      !this.indexBuffer ||
      this.samplerLocation === null
    ) {
      return false;
    }

    const gl = this.gl;
    const viewport = calculateTripleScreenViewport(
      this.canvas.width,
      this.canvas.height,
      this.video.videoWidth,
      this.video.videoHeight
    );
    if (!viewport) return false;

    try {
      if (
        this.video.videoWidth > this.maxTextureSize ||
        this.video.videoHeight > this.maxTextureSize
      ) {
        throw new Error("Video exceeds the WebGL texture size limit");
      }

      gl.viewport(0, 0, this.canvas.width, this.canvas.height);
      gl.clearColor(0, 0, 0, 1);
      gl.clear(gl.COLOR_BUFFER_BIT);
      gl.viewport(
        Math.round(viewport.x),
        Math.round(viewport.y),
        Math.max(1, Math.round(viewport.width)),
        Math.max(1, Math.round(viewport.height))
      );
      gl.useProgram(this.program);

      gl.bindBuffer(gl.ARRAY_BUFFER, this.positionBuffer);
      gl.vertexAttribPointer(
        this.positionLocation,
        2,
        gl.FLOAT,
        false,
        0,
        0
      );
      gl.enableVertexAttribArray(this.positionLocation);

      gl.bindBuffer(gl.ARRAY_BUFFER, this.textureBuffer);
      gl.vertexAttribPointer(
        this.textureLocation,
        2,
        gl.FLOAT,
        false,
        0,
        0
      );
      gl.enableVertexAttribArray(this.textureLocation);
      gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, this.indexBuffer);

      gl.activeTexture(gl.TEXTURE0);
      gl.bindTexture(gl.TEXTURE_2D, this.texture);
      gl.texImage2D(
        gl.TEXTURE_2D,
        0,
        gl.RGBA,
        gl.RGBA,
        gl.UNSIGNED_BYTE,
        this.video
      );
      gl.uniform1i(this.samplerLocation, 0);
      gl.drawElements(gl.TRIANGLES, 18, gl.UNSIGNED_SHORT, 0);

      this.setVisible(true);
      return true;
    } catch (error) {
      this.reportFailure(error);
      return false;
    }
  }

  private scheduleFrame() {
    if (!this.enabledValue || this.destroyed) return;
    if (typeof this.video.requestVideoFrameCallback === "function") {
      if (this.frameCallbackID !== null) return;
      this.frameCallbackID = this.video.requestVideoFrameCallback(() => {
        this.frameCallbackID = null;
        if (!this.enabledValue) return;
        if (!this.drawFrame()) return;
        this.scheduleFrame();
      });
      return;
    }

    if (this.video.paused || this.animationFrameID !== null) return;
    this.animationFrameID = window.requestAnimationFrame(() => {
      this.animationFrameID = null;
      if (!this.enabledValue) return;
      if (!this.drawFrame()) return;
      this.scheduleFrame();
    });
  }

  private cancelFrame() {
    if (
      this.frameCallbackID !== null &&
      typeof this.video.cancelVideoFrameCallback === "function"
    ) {
      this.video.cancelVideoFrameCallback(this.frameCallbackID);
    }
    this.frameCallbackID = null;
    if (this.animationFrameID !== null) {
      window.cancelAnimationFrame(this.animationFrameID);
    }
    this.animationFrameID = null;
  }

  private setVisible(visible: boolean) {
    if (this.visible === visible) return;
    this.visible = visible;
    this.canvas.hidden = !visible;
    this.onVisibilityChange?.(visible);
  }

  private reportFailure(error: unknown) {
    this.enabledValue = false;
    this.cancelFrame();
    this.setVisible(false);
    this.onError?.(
      error instanceof Error ? error : new Error("Triple-screen rendering failed")
    );
  }

  private releaseResources(deleteResources: boolean) {
    const gl = this.gl;
    if (deleteResources && gl && !gl.isContextLost()) {
      if (this.texture) gl.deleteTexture(this.texture);
      if (this.positionBuffer) gl.deleteBuffer(this.positionBuffer);
      if (this.textureBuffer) gl.deleteBuffer(this.textureBuffer);
      if (this.indexBuffer) gl.deleteBuffer(this.indexBuffer);
      if (this.program) gl.deleteProgram(this.program);
    }
    this.gl = null;
    this.program = null;
    this.texture = null;
    this.positionBuffer = null;
    this.textureBuffer = null;
    this.indexBuffer = null;
    this.positionLocation = -1;
    this.textureLocation = -1;
    this.samplerLocation = null;
    this.maxTextureSize = 0;
  }
}

function compileShader(
  gl: WebGLRenderingContext,
  type: number,
  source: string
) {
  const shader = gl.createShader(type);
  if (!shader) throw new Error("WebGL shader allocation failed");
  gl.shaderSource(shader, source);
  gl.compileShader(shader);
  if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) {
    const message = gl.getShaderInfoLog(shader) || "WebGL shader compilation failed";
    gl.deleteShader(shader);
    throw new Error(message);
  }
  return shader;
}

function linkProgram(
  gl: WebGLRenderingContext,
  vertexShader: WebGLShader,
  fragmentShader: WebGLShader
) {
  const program = gl.createProgram();
  if (!program) throw new Error("WebGL program allocation failed");
  gl.attachShader(program, vertexShader);
  gl.attachShader(program, fragmentShader);
  gl.linkProgram(program);
  if (!gl.getProgramParameter(program, gl.LINK_STATUS)) {
    const message = gl.getProgramInfoLog(program) || "WebGL program linking failed";
    gl.deleteProgram(program);
    throw new Error(message);
  }
  return program;
}

function isPositiveFinite(value: number) {
  return Number.isFinite(value) && value > 0;
}

function clamp(value: number, min: number, max: number) {
  return Math.min(max, Math.max(min, value));
}
