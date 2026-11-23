import { fireEvent, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderWithStore } from "../../test/render";
import { testStore } from "../../test/fixtures";
import { ImageViewer } from "./ImageViewer";

const renderViewer = () =>
  renderWithStore(
    <ImageViewer
      name="login-error.png"
      contentType="image/png"
      size={126}
      dataBase64="iVBORw0KGgo="
      byline="added by you, 2m ago"
    />,
    testStore(),
  );

describe("ImageViewer", () => {
  it("shows the image inline with its name as the alt text", () => {
    renderViewer();

    expect(screen.getByRole("img", { name: "login-error.png" })).toHaveAttribute(
      "src",
      "data:image/png;base64,iVBORw0KGgo=",
    );
  });

  it("switches between fitting the pane and showing the image at 100%", async () => {
    renderViewer();

    await userEvent.click(screen.getByRole("button", { name: "100%" }));

    expect(screen.getByRole("button", { name: "100%" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: "Fit" })).toHaveAttribute("aria-pressed", "false");
  });

  it("captures the natural size in the caption once the image loads", () => {
    renderViewer();
    const image = screen.getByRole("img", { name: "login-error.png" });
    Object.defineProperties(image, {
      naturalWidth: { value: 1280 },
      naturalHeight: { value: 800 },
    });

    fireEvent.load(image);

    expect(screen.getByText(/1280×800/)).toBeInTheDocument();
    expect(screen.getByText(/added by you, 2m ago$/)).toBeInTheDocument();
  });
});
