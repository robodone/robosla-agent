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

  // Skip first few frames to make sure we have a stable image.
  for (int i = 0; i < kSkipFirstFrames; i++) {
    dev->wait_for_frames();
  }

  // Save color image.
  cv::Mat color_mat(color_intrinsics.height, color_intrinsics.width, CV_8UC3,
		    const_cast<void*>(dev->get_frame_data(rs::stream::color)));
  cv::cvtColor(color_mat, color_mat, CV_RGB2BGR);
  std::vector<int> color_params = { CV_IMWRITE_JPEG_QUALITY, 90 };
  // TODO(krasin): handle failures.
  cv::imwrite("color.jpg", color_mat, color_params);

  // Save depth image.
  cv::Mat depth_mat(depth_intrinsics.height, depth_intrinsics.width, CV_16UC1,
		    const_cast<void*>(dev->get_frame_data(rs::stream::depth)));
  std::vector<int> depth_params = { CV_IMWRITE_PNG_COMPRESSION, 9 };
  // TODO(krasin): handle failures.
  cv::imwrite("depth.png", depth_mat, depth_params);
  return 0;
}
