#include <stdio.h>
#include <unistd.h>

#include <vector>

#include <librealsense/rs.hpp>
#include <opencv2/opencv.hpp>

const int kSkipFirstFrames = 60;

void fail(const char* msg) {
  fprintf(stderr, "%s\n", msg);
  _exit(1);
}

int main(void) {
  rs::log_to_console(rs::log_severity::warn);

  // Create a context.
  rs::context ctx;

  // Find and open a device.
  if (!ctx.get_device_count()) {
    fail("No RealSense devices connected");
  }

  rs::device * dev = ctx.get_device(0);
  fprintf(stderr, "RealSense device opened: %s, SN %s, firmware version %s\n",
	  dev->get_name(), dev->get_serial(), dev->get_firmware_version());

  // Check that the camera supports color and depth streams and enable them.
  if (!dev->supports(rs::capabilities::color)) {
    fail("Device does not support RGB stream");
  }
  if (!dev->supports(rs::capabilities::depth)) {
    fail("Device does not support Depth stream");
  }

  dev->enable_stream(rs::stream::color, rs::preset::best_quality);
  dev->enable_stream(rs::stream::depth, rs::preset::best_quality);

  // Start streaming.
  dev->start();

  // Get stream params (including width and height of each frame).
  rs::intrinsics color_intrinsics = dev->get_stream_intrinsics(rs::stream::color);
  rs::intrinsics depth_intrinsics = dev->get_stream_intrinsics(rs::stream::depth);

  size_t color_buf_size = color_intrinsics.height * color_intrinsics.width * 3;
  size_t depth_buf_size = depth_intrinsics.height * depth_intrinsics.width * 2;

  std::vector<uint8_t> color_buf(color_buf_size);
  std::vector<uint8_t> depth_buf(depth_buf_size);

  // Skip first few frames to make sure we have a stable image.
  for (int i = 0; i < kSkipFirstFrames; i++) {
    dev->wait_for_frames();
  }

  // Copy frames to the buffers, so we can modify the contents and be sure that
  // the new frame won't corrupt the data.
  memcpy(color_buf.data(), dev->get_frame_data(rs::stream::color), color_buf_size);
  memcpy(depth_buf.data(), dev->get_frame_data(rs::stream::depth), depth_buf_size);

  // Save the color frame as a JPEG image.
  cv::Mat color_mat(color_intrinsics.height, color_intrinsics.width, CV_8UC3, color_buf.data());
  cv::cvtColor(color_mat, color_mat, CV_RGB2BGR);
  std::vector<int> color_params = { CV_IMWRITE_JPEG_QUALITY, 90 };
  if (!cv::imwrite("color.jpg", color_mat, color_params)) {
    fail("Failed to save color frame");
  }

  // Save the depth frame as a 16-bit grayscale PNG image.
  cv::Mat depth_mat(depth_intrinsics.height, depth_intrinsics.width, CV_16UC1, depth_buf.data());
  std::vector<int> depth_params = { CV_IMWRITE_PNG_COMPRESSION, 9 };
  if (!cv::imwrite("depth.png", depth_mat, depth_params)) {
    fail("Failed to save depth frame");
  }
  return 0;
}
