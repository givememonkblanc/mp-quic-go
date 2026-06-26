#include "camera_wrapper.h"

#include <opencv2/opencv.hpp>

#include <cstdio>
#include <cstring>
#include <cstdlib>
#include <vector>

extern "C" {

namespace {

struct CamCtx {
    cv::VideoCapture* cap;
    int is_depth;
    int width;
    int height;

    CamCtx() : cap(nullptr), is_depth(0), width(0), height(0) {}
};

} // anonymous namespace

cam_handle_t cam_open(int index, int is_depth) {
    CamCtx* ctx = new CamCtx();
    ctx->is_depth = is_depth;

    cv::VideoCapture* cap = new cv::VideoCapture(index, cv::CAP_V4L2);
    if (!cap->isOpened()) {
        std::fprintf(stderr, "[cam_wrapper] /dev/video%d: open failed\n", index);
        delete cap;
        delete ctx;
        return nullptr;
    }

    if (is_depth) {
        // Raw 16-bit — do NOT let OpenCV convert to 8-bit BGR
        cap->set(cv::CAP_PROP_CONVERT_RGB, 0.0);

        cv::Mat probe;
        if (!cap->read(probe) || probe.empty()) {
            std::fprintf(stderr, "[cam_wrapper] /dev/video%d: depth probe frame failed\n", index);
            cap->release();
            delete cap;
            delete ctx;
            return nullptr;
        }

        // Confirm 16-bit single-channel
        if (probe.depth() != CV_16U || probe.channels() != 1) {
            std::fprintf(stderr, "[cam_wrapper] /dev/video%d: not 16-bit depth (depth=%d ch=%d)\n",
                         index, probe.depth(), probe.channels());
            cap->release();
            delete cap;
            delete ctx;
            return nullptr;
        }

        ctx->width  = probe.cols;
        ctx->height = probe.rows;
        std::fprintf(stderr, "[cam_wrapper] depth /dev/video%d: %dx%d 16-bit\n",
                     index, ctx->width, ctx->height);

        // Re-open cleanly for capture
        cap->release();
        delete cap;

        cap = new cv::VideoCapture(index, cv::CAP_V4L2);
        if (!cap->isOpened()) {
            std::fprintf(stderr, "[cam_wrapper] /dev/video%d: re-open failed\n", index);
            delete ctx;
            return nullptr;
        }
        cap->set(cv::CAP_PROP_CONVERT_RGB, 0.0);
    } else {
        // RGB: request MJPEG, 640x480, 30fps
        cap->set(cv::CAP_PROP_FOURCC,
                 cv::VideoWriter::fourcc('M', 'J', 'P', 'G'));
        cap->set(cv::CAP_PROP_FRAME_WIDTH, 640.0);
        cap->set(cv::CAP_PROP_FRAME_HEIGHT, 480.0);
        cap->set(cv::CAP_PROP_FPS, 30.0);

        cv::Mat probe;
        if (!cap->read(probe) || probe.empty()) {
            std::fprintf(stderr, "[cam_wrapper] /dev/video%d: RGB probe frame failed\n", index);
            cap->release();
            delete cap;
            delete ctx;
            return nullptr;
        }

        ctx->width  = probe.cols;
        ctx->height = probe.rows;
        std::fprintf(stderr, "[cam_wrapper] RGB /dev/video%d: %dx%d\n",
                     index, ctx->width, ctx->height);
    }

    // Keep only the most recent frame in the driver/OpenCV buffer. The consumer
    // (ack-synchronous streaming) is much slower than the 30 fps camera, so with
    // the default multi-frame buffer each read() returns an old, queued frame and
    // the transmitted video lags further and further behind real time. With a
    // 1-frame buffer every read() returns the latest captured frame.
    cap->set(cv::CAP_PROP_BUFFERSIZE, 1.0);

    ctx->cap = cap;

    // Warm-up: grab a few frames to stabilize
    for (int i = 0; i < 3; i++) {
        cv::Mat warmup;
        if (!cap->read(warmup)) break;
    }

    std::fprintf(stderr, "[cam_wrapper] /dev/video%d ready (is_depth=%d %dx%d)\n",
                 index, is_depth, ctx->width, ctx->height);
    return static_cast<cam_handle_t>(ctx);
}

int cam_capture(cam_handle_t handle, unsigned char* buffer, int buf_size) {
    if (!handle) return -1;
    CamCtx* ctx = static_cast<CamCtx*>(handle);
    if (!ctx->cap) return -1;

    cv::Mat frame;
    if (!ctx->cap->read(frame) || frame.empty()) {
        std::fprintf(stderr, "[cam_wrapper] frame capture failed\n");
        return -2;
    }

    if (ctx->is_depth) {
        // Depth: normalize 16-bit → 8-bit → colormap JPEG
        cv::Mat gray8;
        if (frame.depth() == CV_16U && frame.channels() == 1) {
            // Percentile normalization (2nd–98th)
            cv::Mat flat = frame.reshape(1, ctx->width * ctx->height);
            cv::Mat sorted;
            cv::sort(flat, sorted, cv::SORT_EVERY_COLUMN + cv::SORT_ASCENDING);
            int n = ctx->width * ctx->height;
            uint16_t* sp = sorted.ptr<uint16_t>(0);
            uint16_t vmin = sp[n / 50];
            uint16_t vmax = sp[(n * 49) / 50];
            if (vmax <= vmin) vmax = vmin + 1;
            frame.convertTo(gray8, CV_8U, 255.0 / (vmax - vmin), -vmin * 255.0 / (vmax - vmin));
        } else {
            frame.convertTo(gray8, CV_8U);
        }

        cv::Mat color;
        cv::applyColorMap(gray8, color, cv::COLORMAP_JET);

        std::vector<int> jpg_params;
        jpg_params.push_back(cv::IMWRITE_JPEG_QUALITY);
        jpg_params.push_back(85);
        std::vector<uchar> enc_buf;
        if (!cv::imencode(".jpg", color, enc_buf, jpg_params)) {
            return -3;
        }
        if ((int)enc_buf.size() > buf_size) {
            return -4;
        }
        std::memcpy(buffer, enc_buf.data(), enc_buf.size());
        return static_cast<int>(enc_buf.size());
    } else {
        // RGB: BGR → JPEG
        std::vector<int> jpg_params;
        jpg_params.push_back(cv::IMWRITE_JPEG_QUALITY);
        jpg_params.push_back(70);
        std::vector<uchar> enc_buf;
        if (!cv::imencode(".jpg", frame, enc_buf, jpg_params)) {
            return -3;
        }
        if ((int)enc_buf.size() > buf_size) {
            return -4;
        }
        std::memcpy(buffer, enc_buf.data(), enc_buf.size());
        return static_cast<int>(enc_buf.size());
    }
}

int cam_get_width(cam_handle_t handle) {
    if (!handle) return 0;
    return static_cast<CamCtx*>(handle)->width;
}

int cam_get_height(cam_handle_t handle) {
    if (!handle) return 0;
    return static_cast<CamCtx*>(handle)->height;
}

void cam_close(cam_handle_t handle) {
    if (!handle) return;
    CamCtx* ctx = static_cast<CamCtx*>(handle);
    if (ctx->cap) {
        ctx->cap->release();
        delete ctx->cap;
    }
    delete ctx;
}

int cam_is_depth_device(int index) {
    char path[128];
    int ret = snprintf(path, sizeof(path),
                       "/sys/class/video4linux/video%d/name", index);
    if (ret <= 0 || ret >= (int)sizeof(path)) return 0;

    FILE* f = fopen(path, "r");
    if (!f) return 0;

    char name[256];
    if (!fgets(name, (int)sizeof(name), f)) {
        fclose(f);
        return 0;
    }
    fclose(f);

    // If the name contains "Depth" (case-sensitive), it's a depth sensor.
    return (strstr(name, "Depth") != nullptr) ? 1 : 0;
}

} // extern "C"
