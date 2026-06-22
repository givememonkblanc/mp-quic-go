#ifndef CAMERA_WRAPPER_H
#define CAMERA_WRAPPER_H

#ifdef __cplusplus
extern "C" {
#endif

/* Opaque handle to a camera context */
typedef void* cam_handle_t;

/* Frame type constants */
#define CAM_FRAME_RGB   0
#define CAM_FRAME_DEPTH 1

/**
 * Open a V4L2 camera via OpenCV.
 * @param index  /dev/videoN device index
 * @param is_depth  1 = depth (16-bit raw, PNG/colormap JPEG),
 *                   0 = RGB (MJPEG → JPEG)
 * @return camera handle, or NULL on failure
 */
cam_handle_t cam_open(int index, int is_depth);

/**
 * Capture one frame and encode it.
 * @param handle   camera handle from cam_open()
 * @param buffer   output buffer for encoded JPEG/PNG data
 * @param buf_size capacity of buffer
 * @return number of bytes written to buffer, or negative on error:
 *         -1 = null handle
 *         -2 = frame capture failed
 *         -3 = encode failed
 *         -4 = buffer too small
 */
int cam_capture(cam_handle_t handle, unsigned char* buffer, int buf_size);

/**
 * Get the width of the opened camera.
 * @return width in pixels, or 0 on error
 */
int cam_get_width(cam_handle_t handle);

/**
 * Get the height of the opened camera.
 * @return height in pixels, or 0 on error
 */
int cam_get_height(cam_handle_t handle);

/**
 * Close and release the camera.
 * @param handle camera handle to close
 */
void cam_close(cam_handle_t handle);

/**
 * Check if a video device is a depth sensor (not RGB) by reading its sysfs name.
 * @param index  /dev/videoN device index
 * @return 1 if device name contains "Depth", 0 if not or on error
 */
int cam_is_depth_device(int index);

#ifdef __cplusplus
}
#endif

#endif /* CAMERA_WRAPPER_H */
